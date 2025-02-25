// Copyright 2019 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package kvcoord

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/kv"
	"github.com/cockroachdb/cockroach/pkg/kv/kvpb"
	"github.com/cockroachdb/cockroach/pkg/kv/kvserver"
	"github.com/cockroachdb/cockroach/pkg/kv/kvserver/concurrency/lock"
	"github.com/cockroachdb/cockroach/pkg/kv/kvserver/kvserverbase"
	"github.com/cockroachdb/cockroach/pkg/roachpb"
	"github.com/cockroachdb/cockroach/pkg/testutils/datapathutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/kvclientutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/serverutils"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/datadriven"
	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
)

func TestSavepoints(t *testing.T) {
	defer leaktest.AfterTest(t)()
	defer log.Scope(t).Close(t)

	ctx := context.Background()
	abortKey := roachpb.Key("abort")
	errKey := roachpb.Key("injectErr")

	datadriven.Walk(t, datapathutils.TestDataPath(t, "savepoints"), func(t *testing.T, path string) {
		// We want to inject txn abort errors in some cases.
		//
		// We do this by injecting the error from "underneath" the
		// TxnCoordSender, from storage.
		params := base.TestServerArgs{}
		var doAbort int64
		params.Knobs.Store = &kvserver.StoreTestingKnobs{
			EvalKnobs: kvserverbase.BatchEvalTestingKnobs{
				TestingEvalFilter: func(args kvserverbase.FilterArgs) *kvpb.Error {
					key := args.Req.Header().Key
					if atomic.LoadInt64(&doAbort) != 0 && key.Equal(abortKey) {
						return kvpb.NewErrorWithTxn(
							kvpb.NewTransactionAbortedError(kvpb.ABORT_REASON_UNKNOWN), args.Hdr.Txn)
					}
					if key.Equal(errKey) {
						return kvpb.NewErrorf("injected error")
					}
					return nil
				},
			},
		}

		// New database for each test file.
		s, _, db := serverutils.StartServer(t, params)
		defer s.Stopper().Stop(ctx)

		// Transient state during the test.
		sp := make(map[string]kv.SavepointToken)
		var txn *kv.Txn

		datadriven.RunTest(t, path, func(t *testing.T, td *datadriven.TestData) string {
			var buf strings.Builder

			ptxn := func() {
				tc := txn.Sender().(*TxnCoordSender)
				fmt.Fprintf(&buf, "%d ", tc.interceptorAlloc.txnSeqNumAllocator.writeSeq)
				if len(tc.mu.txn.IgnoredSeqNums) == 0 {
					buf.WriteString("<noignore>")
				}
				for _, r := range tc.mu.txn.IgnoredSeqNums {
					fmt.Fprintf(&buf, "[%d-%d]", r.Start, r.End)
				}
				fmt.Fprintln(&buf)
			}

			switch td.Cmd {
			case "begin":
				txn = kv.NewTxn(ctx, db, 0)
				ptxn()

			case "commit":
				if err := txn.Commit(ctx); err != nil {
					fmt.Fprintf(&buf, "(%T) %v\n", err, err)
				}

			case "retry":
				epochBefore := txn.Epoch()
				retryErr := txn.GenerateForcedRetryableErr(ctx, "forced retry")
				epochAfter := txn.Epoch()
				fmt.Fprintf(&buf, "synthetic error: %v\n", retryErr)
				fmt.Fprintf(&buf, "epoch: %d -> %d\n", epochBefore, epochAfter)

			// inject-error runs a Get with an untyped error injected into request
			// evaluation.
			case "inject-error":
				_, err := txn.Get(ctx, errKey)
				require.Regexp(t, "injected error", err.Error())
				fmt.Fprint(&buf, "injected error\n")

			case "abort":
				prevID := txn.ID()
				atomic.StoreInt64(&doAbort, 1)
				defer func() { atomic.StoreInt64(&doAbort, 00) }()
				err := txn.Put(ctx, abortKey, []byte("value"))
				fmt.Fprintf(&buf, "(%T)\n", err)
				changed := "changed"
				if prevID == txn.ID() {
					changed = "not changed"
				}
				fmt.Fprintf(&buf, "txn id %s\n", changed)

			case "reset":
				prevID := txn.ID()
				if err := txn.PrepareForRetry(ctx); err != nil {
					t.Fatal(err)
				}
				changed := "changed"
				if prevID == txn.ID() {
					changed = "not changed"
				}
				fmt.Fprintf(&buf, "txn error cleared\n")
				fmt.Fprintf(&buf, "txn id %s\n", changed)

			case "put":
				b := txn.NewBatch()
				b.Put(td.CmdArgs[0].Key, td.CmdArgs[1].Key)
				if td.HasArg("nowait") {
					b.Header.WaitPolicy = lock.WaitPolicy_Error
				}
				if td.HasArg("lock-timeout") {
					b.Header.LockTimeout = 1 * time.Nanosecond
				}
				if err := txn.Run(ctx, b); err != nil {
					fmt.Fprintf(&buf, "(%T) %v\n", err, err)
				}

			// cput takes <key> <value> <expected value>. The expected value can be
			// "nil".
			case "cput":
				expS := td.CmdArgs[2].Key
				var expVal []byte
				if expS != "nil" {
					expVal = kvclientutils.StrToCPutExistingValue(expS)
				}
				if err := txn.CPut(ctx,
					roachpb.Key(td.CmdArgs[0].Key),
					[]byte(td.CmdArgs[1].Key),
					expVal,
				); err != nil {
					if errors.HasType(err, (*kvpb.ConditionFailedError)(nil)) {
						// Print an easier to match message.
						fmt.Fprintf(&buf, "(%T) unexpected value\n", err)
					} else {
						fmt.Fprintf(&buf, "(%T) %v\n", err, err)
					}
				}

			case "get":
				b := txn.NewBatch()
				if td.HasArg("locking") {
					b.GetForUpdate(td.CmdArgs[0].Key, kvpb.BestEffort)
				} else {
					b.Get(td.CmdArgs[0].Key)
				}
				if td.HasArg("nowait") {
					b.Header.WaitPolicy = lock.WaitPolicy_Error
				}
				if td.HasArg("lock-timeout") {
					b.Header.LockTimeout = 1 * time.Nanosecond
				}
				if err := txn.Run(ctx, b); err != nil {
					fmt.Fprintf(&buf, "(%T) %v\n", err, err)
				} else {
					kv := b.Results[0].Rows[0]
					ba, err := kv.Value.GetBytes()
					require.NoError(t, err)
					fmt.Fprintf(&buf, "%v -> %v\n", kv.Key, string(ba))
				}

			case "savepoint":
				spt, err := txn.CreateSavepoint(ctx)
				if err != nil {
					fmt.Fprintf(&buf, "(%T) %v\n", err, err)
				} else {
					sp[td.CmdArgs[0].Key] = spt
					ptxn()
				}

			case "release":
				spn := td.CmdArgs[0].Key
				spt := sp[spn]
				if err := txn.ReleaseSavepoint(ctx, spt); err != nil {
					fmt.Fprintf(&buf, "(%T) %v\n", err, err)
				} else {
					ptxn()
				}

			case "rollback":
				spn := td.CmdArgs[0].Key
				spt := sp[spn]
				if err := txn.RollbackToSavepoint(ctx, spt); err != nil {
					fmt.Fprintf(&buf, "(%T) %v\n", err, err)
				} else {
					ptxn()
				}

			default:
				td.Fatalf(t, "unknown directive: %s", td.Cmd)
			}
			return buf.String()
		})
	})
}
