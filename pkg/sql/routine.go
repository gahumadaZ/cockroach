// Copyright 2022 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package sql

import (
	"context"
	"strconv"

	"github.com/cockroachdb/cockroach/pkg/kv"
	"github.com/cockroachdb/cockroach/pkg/sql/catalog/colinfo"
	"github.com/cockroachdb/cockroach/pkg/sql/isql"
	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgcode"
	"github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgerror"
	"github.com/cockroachdb/cockroach/pkg/sql/plpgsql"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/eval"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/sql/types"
	"github.com/cockroachdb/cockroach/pkg/util"
	"github.com/cockroachdb/cockroach/pkg/util/errorutil/unimplemented"
	"github.com/cockroachdb/cockroach/pkg/util/timeutil"
	"github.com/cockroachdb/cockroach/pkg/util/tracing"
	"github.com/cockroachdb/errors"
)

// A callNode executes a procedure.
type callNode struct {
	proc *tree.RoutineExpr
}

var _ planNode = &callNode{}

// startExec implements the planNode interface.
func (d *callNode) startExec(params runParams) error {
	// Until OUT and INOUT parameters are supported, all procedures return no
	// results, so we can ignore the results of the routine.
	_, err := eval.Expr(params.ctx, params.EvalContext(), d.proc)
	return err
}

// Next implements the planNode interface.
func (d *callNode) Next(params runParams) (bool, error) { return false, nil }

// Values implements the planNode interface.
func (d *callNode) Values() tree.Datums { return nil }

// Close implements the planNode interface.
func (d *callNode) Close(ctx context.Context) {}

// EvalRoutineExpr returns the result of evaluating the routine. It calls the
// routine's ForEachPlan closure to generate a plan for each statement in the
// routine, then runs the plans. The resulting value of the last statement in
// the routine is returned.
func (p *planner) EvalRoutineExpr(
	ctx context.Context, expr *tree.RoutineExpr, args tree.Datums,
) (result tree.Datum, err error) {
	// Strict routines (CalledOnNullInput=false) should not be invoked and they
	// should immediately return NULL if any of their arguments are NULL.
	if !expr.CalledOnNullInput {
		for i := range args {
			if args[i] == tree.DNull {
				return tree.DNull, nil
			}
		}
	}

	// Return the cached result if it exists.
	if expr.CachedResult != nil {
		return expr.CachedResult, nil
	}

	if expr.TailCall && !expr.Generator && p.EvalContext().RoutineSender != nil {
		// This is a nested routine in tail-call position.
		if !p.curPlan.flags.IsDistributed() && tailCallOptimizationEnabled {
			// Tail-call optimizations are enabled. Send the information needed to
			// evaluate this routine to the parent routine, then return. It is safe to
			// return NULL here because the parent is guaranteed not to perform any
			// processing on the result of the child.
			p.EvalContext().RoutineSender.SendDeferredRoutine(expr, args)
			return tree.DNull, nil
		}
	}

	var g routineGenerator
	g.init(p, expr, args)
	defer g.Close(ctx)
	err = g.Start(ctx, p.Txn())
	if err != nil {
		return nil, err
	}

	hasNext, err := g.Next(ctx)
	if err != nil {
		return nil, err
	}

	var res tree.Datum
	if !hasNext {
		// The result is NULL if no rows were returned by the last statement in
		// the routine.
		res = tree.DNull
	} else {
		// The result is the first and only column in the row returned by the
		// last statement in the routine.
		row, err := g.Values()
		if err != nil {
			return nil, err
		}
		res = row[0]
	}
	if len(args) == 0 && !expr.EnableStepping {
		// Cache the result if there are zero arguments and stepping is
		// disabled.
		expr.CachedResult = res
	}
	return res, nil
}

// RoutineExprGenerator returns an eval.ValueGenerator that produces the results
// of a routine.
func (p *planner) RoutineExprGenerator(
	ctx context.Context, expr *tree.RoutineExpr, args tree.Datums,
) eval.ValueGenerator {
	var g routineGenerator
	g.init(p, expr, args)
	return &g
}

// routineGenerator is an eval.ValueGenerator that produces the result of a
// routine.
type routineGenerator struct {
	p        *planner
	expr     *tree.RoutineExpr
	args     tree.Datums
	rch      rowContainerHelper
	rci      *rowContainerIterator
	currVals tree.Datums
	// deferredRoutine encapsulates the information needed to execute a nested
	// routine that has deferred its execution.
	deferredRoutine struct {
		expr *tree.RoutineExpr
		args tree.Datums
	}
}

var _ eval.ValueGenerator = &routineGenerator{}
var _ eval.DeferredRoutineSender = &routineGenerator{}

// init initializes a routineGenerator.
func (g *routineGenerator) init(p *planner, expr *tree.RoutineExpr, args tree.Datums) {
	*g = routineGenerator{
		p:    p,
		expr: expr,
		args: args,
	}
}

// reset closes and re-initializes a routineGenerator for reuse.
// TODO(drewk): we should hold on to memory for the row container.
func (g *routineGenerator) reset(
	ctx context.Context, p *planner, expr *tree.RoutineExpr, args tree.Datums,
) {
	g.Close(ctx)
	g.init(p, expr, args)
}

// ResolvedType is part of the ValueGenerator interface.
func (g *routineGenerator) ResolvedType() *types.T {
	return g.expr.ResolvedType()
}

// Start is part of the ValueGenerator interface.
func (g *routineGenerator) Start(ctx context.Context, txn *kv.Txn) (err error) {
	for {
		err = g.startInternal(ctx, txn)
		if err != nil || g.deferredRoutine.expr == nil {
			// No tail-call optimization.
			return err
		}
		// A nested routine in tail-call position deferred its execution until now.
		// Since it's in tail-call position, evaluating it will give the result of
		// this routine as well.
		g.reset(ctx, g.p, g.deferredRoutine.expr, g.deferredRoutine.args)
	}
}

// startInternal implements logic for a single execution of a routine.
// TODO(mgartner): We can cache results for future invocations of the routine by
// creating a new iterator over an existing row container helper if the routine
// is cache-able (i.e., there are no arguments to the routine and stepping is
// disabled).
func (g *routineGenerator) startInternal(ctx context.Context, txn *kv.Txn) (err error) {
	rt := g.expr.ResolvedType()
	var retTypes []*types.T
	if g.expr.MultiColOutput {
		// A routine with multiple output column should have its types in a tuple.
		if rt.Family() != types.TupleFamily {
			return errors.AssertionFailedf("routine expected to return multiple columns")
		}
		retTypes = rt.TupleContents()
	} else {
		retTypes = []*types.T{g.expr.ResolvedType()}
	}
	g.rch.Init(ctx, retTypes, g.p.ExtendedEvalContext(), "routine" /* opName */)

	// Configure stepping for volatile routines so that mutations made by the
	// invoking statement are visible to the routine.
	if g.expr.EnableStepping {
		prevSteppingMode := txn.ConfigureStepping(ctx, kv.SteppingEnabled)
		prevSeqNum := txn.GetReadSeqNum()
		defer func() {
			// If the routine errored, the transaction should be aborted, so
			// there is no need to reconfigure stepping or revert to the
			// original sequence number.
			if err == nil {
				_ = txn.ConfigureStepping(ctx, prevSteppingMode)
				err = txn.SetReadSeqNum(prevSeqNum)
			}
		}()
	}

	// Execute each statement in the routine sequentially.
	stmtIdx := 0
	ef := newExecFactory(ctx, g.p)
	rrw := NewRowResultWriter(&g.rch)
	var cursorHelper *plpgsqlCursorHelper
	err = g.expr.ForEachPlan(ctx, ef, g.args, func(plan tree.RoutinePlan, isFinalPlan bool) error {
		stmtIdx++
		opName := "udf-stmt-" + g.expr.Name + "-" + strconv.Itoa(stmtIdx)
		ctx, sp := tracing.ChildSpan(ctx, opName)
		defer sp.Finish()

		var w rowResultWriter
		openCursor := stmtIdx == 1 && g.expr.CursorDeclaration != nil
		if isFinalPlan && !g.expr.Procedure {
			// The result of this statement is the routine's output. This is never the
			// case for a procedure, which does not output any rows (since we do not
			// yet support OUT or INOUT parameters).
			w = rrw
		} else if openCursor {
			// The result of the first statement will be used to open a SQL cursor.
			cursorHelper, err = g.newCursorHelper(ctx, plan.(*planComponents))
			if err != nil {
				return err
			}
			w = NewRowResultWriter(&cursorHelper.container)
		} else {
			// The result of this statement is not needed. Use a rowResultWriter that
			// drops all rows added to it.
			w = &droppingResultWriter{}
		}

		// Place a sequence point before each statement in the routine for
		// volatile functions.
		if g.expr.EnableStepping {
			if err := txn.Step(ctx); err != nil {
				return err
			}
		}

		// Run the plan.
		err = runPlanInsidePlan(ctx, g.p.RunParams(ctx), plan.(*planComponents), w, g)
		if err != nil {
			return err
		}
		if openCursor {
			return cursorHelper.createCursor(g.p)
		}
		return nil
	})
	if err != nil {
		if cursorHelper != nil {
			err = errors.CombineErrors(err, cursorHelper.Close())
		}
		return g.handleException(ctx, err)
	}

	g.rci = newRowContainerIterator(ctx, g.rch)
	return nil
}

// handleException attempts to match the code of the given error to an exception
// handler for the routine. If the error finds a match, the corresponding branch
// for the exception handler is executed as a routine.
// TODO(drewk): When there is an exception block, we need to nest the body of
// the routine in a sub-transaction, probably using savepoints. If an error
// occurs in the body of a block with an exception handler, changes to the
// database that happened within the block should be rolled back, but not those
// that occurred outside the block.
func (g *routineGenerator) handleException(ctx context.Context, err error) error {
	if plpgsql.IsCaughtRoutineException(err) {
		// This error has already been through handleException in a nested call.
		return err
	}
	caughtCode := pgerror.GetPGCode(err)
	if caughtCode == pgcode.Uncategorized {
		return err
	}
	if handler := g.expr.ExceptionHandler; handler != nil {
		for i, code := range handler.Codes {
			if code == caughtCode {
				g.reset(ctx, g.p, handler.Actions[i], g.args)
				err = g.startInternal(ctx, g.p.Txn())
				break
			}
		}
		if err != nil {
			return plpgsql.NewCaughtRoutineException(err)
		}
	}
	// No exception handler matched.
	return err
}

// Next is part of the ValueGenerator interface.
func (g *routineGenerator) Next(ctx context.Context) (bool, error) {
	var err error
	g.currVals, err = g.rci.Next()
	if err != nil {
		return false, err
	}
	return g.currVals != nil, nil
}

// Values is part of the ValueGenerator interface.
func (g *routineGenerator) Values() (tree.Datums, error) {
	return g.currVals, nil
}

// Close is part of the ValueGenerator interface.
func (g *routineGenerator) Close(ctx context.Context) {
	if g.rci != nil {
		g.rci.Close()
	}
	g.rch.Close(ctx)
	*g = routineGenerator{}
}

var tailCallOptimizationEnabled = util.ConstantWithMetamorphicTestBool(
	"tail-call-optimization-enabled",
	true,
)

func (g *routineGenerator) SendDeferredRoutine(routine *tree.RoutineExpr, args tree.Datums) {
	g.deferredRoutine.expr = routine
	g.deferredRoutine.args = args
}

// droppingResultWriter drops all rows that are added to it. It only tracks
// errors with the SetError and Err functions.
type droppingResultWriter struct {
	err error
}

// AddRow is part of the rowResultWriter interface.
func (d *droppingResultWriter) AddRow(ctx context.Context, row tree.Datums) error {
	return nil
}

// SetRowsAffected is part of the rowResultWriter interface.
func (d *droppingResultWriter) SetRowsAffected(ctx context.Context, n int) {}

// SetError is part of the rowResultWriter interface.
func (d *droppingResultWriter) SetError(err error) {
	d.err = err
}

// Err is part of the rowResultWriter interface.
func (d *droppingResultWriter) Err() error {
	return d.err
}

func (g *routineGenerator) newCursorHelper(
	ctx context.Context, plan *planComponents,
) (*plpgsqlCursorHelper, error) {
	open := g.expr.CursorDeclaration
	if open.NameArgIdx < 0 || open.NameArgIdx >= len(g.args) {
		panic(errors.AssertionFailedf("unexpected name argument index: %d", open.NameArgIdx))
	}
	if g.args[open.NameArgIdx] == tree.DNull {
		return nil, unimplemented.New("unnamed cursor",
			"opening an unnamed cursor is not yet supported",
		)
	}
	planCols := plan.main.planColumns()
	cursorHelper := &plpgsqlCursorHelper{
		ctx:        ctx,
		cursorName: tree.Name(tree.MustBeDString(g.args[open.NameArgIdx])),
		resultCols: make(colinfo.ResultColumns, len(planCols)),
	}
	copy(cursorHelper.resultCols, planCols)
	cursorHelper.container.Init(
		ctx,
		getTypesFromResultColumns(planCols),
		g.p.ExtendedEvalContextCopy(),
		"routine_open_cursor", /* opName */
	)
	return cursorHelper, nil
}

// plpgsqlCursorHelper wraps a row container in order to feed the results of
// executing a SQL statement to a SQL cursor. Note that the SQL statement is not
// lazily executed; its entire result is written to the container.
// TODO(drewk): while the row container can spill to disk, we should default to
// lazy execution for cursors for performance reasons.
type plpgsqlCursorHelper struct {
	ctx        context.Context
	cursorName tree.Name
	cursorSql  string

	// Fields related to implementing the isql.Rows interface.
	container    rowContainerHelper
	iter         *rowContainerIterator
	resultCols   colinfo.ResultColumns
	lastRow      tree.Datums
	lastErr      error
	rowsAffected int
}

func (h *plpgsqlCursorHelper) createCursor(p *planner) error {
	h.iter = newRowContainerIterator(h.ctx, h.container)
	cursor := &sqlCursor{
		Rows:       h,
		readSeqNum: p.txn.GetReadSeqNum(),
		txn:        p.txn,
		statement:  h.cursorSql,
		created:    timeutil.Now(),
	}
	if err := p.checkIfCursorExists(h.cursorName); err != nil {
		return err
	}
	return p.sqlCursors.addCursor(h.cursorName, cursor)
}

var _ isql.Rows = &plpgsqlCursorHelper{}

// Next implements the isql.Rows interface.
func (h *plpgsqlCursorHelper) Next(_ context.Context) (bool, error) {
	h.lastRow, h.lastErr = h.iter.Next()
	if h.lastErr != nil {
		return false, h.lastErr
	}
	if h.lastRow != nil {
		h.rowsAffected++
	}
	return h.lastRow != nil, nil
}

// Cur implements the isql.Rows interface.
func (h *plpgsqlCursorHelper) Cur() tree.Datums {
	return h.lastRow
}

// RowsAffected implements the isql.Rows interface.
func (h *plpgsqlCursorHelper) RowsAffected() int {
	return h.rowsAffected
}

// Close implements the isql.Rows interface.
func (h *plpgsqlCursorHelper) Close() error {
	if h.iter != nil {
		h.iter.Close()
		h.iter = nil
	}
	h.container.Close(h.ctx)
	return h.lastErr
}

// Types implements the isql.Rows interface.
func (h *plpgsqlCursorHelper) Types() colinfo.ResultColumns {
	return h.resultCols
}

// HasResults implements the isql.Rows interface.
func (h *plpgsqlCursorHelper) HasResults() bool {
	return h.lastRow != nil
}
