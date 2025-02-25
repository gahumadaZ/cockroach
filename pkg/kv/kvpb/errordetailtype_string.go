// Code generated by "stringer"; DO NOT EDIT.

package kvpb

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[NotLeaseHolderErrType-1]
	_ = x[RangeNotFoundErrType-2]
	_ = x[RangeKeyMismatchErrType-3]
	_ = x[ReadWithinUncertaintyIntervalErrType-4]
	_ = x[TransactionAbortedErrType-5]
	_ = x[TransactionPushErrType-6]
	_ = x[TransactionRetryErrType-7]
	_ = x[TransactionStatusErrType-8]
	_ = x[LockConflictErrType-9]
	_ = x[WriteTooOldErrType-10]
	_ = x[OpRequiresTxnErrType-11]
	_ = x[ConditionFailedErrType-12]
	_ = x[LeaseRejectedErrType-13]
	_ = x[NodeUnavailableErrType-14]
	_ = x[RaftGroupDeletedErrType-16]
	_ = x[ReplicaCorruptionErrType-17]
	_ = x[ReplicaTooOldErrType-18]
	_ = x[AmbiguousResultErrType-26]
	_ = x[StoreNotFoundErrType-27]
	_ = x[TransactionRetryWithProtoRefreshErrType-28]
	_ = x[IntegerOverflowErrType-31]
	_ = x[UnsupportedRequestErrType-32]
	_ = x[BatchTimestampBeforeGCErrType-34]
	_ = x[TxnAlreadyEncounteredErrType-35]
	_ = x[IntentMissingErrType-36]
	_ = x[MergeInProgressErrType-37]
	_ = x[RangeFeedRetryErrType-38]
	_ = x[IndeterminateCommitErrType-39]
	_ = x[InvalidLeaseErrType-40]
	_ = x[OptimisticEvalConflictsErrType-41]
	_ = x[MinTimestampBoundUnsatisfiableErrType-42]
	_ = x[RefreshFailedErrType-43]
	_ = x[MVCCHistoryMutationErrType-44]
	_ = x[CommunicationErrType-22]
	_ = x[InternalErrType-25]
}

func (i ErrorDetailType) String() string {
	switch i {
	case NotLeaseHolderErrType:
		return "NotLeaseHolderErrType"
	case RangeNotFoundErrType:
		return "RangeNotFoundErrType"
	case RangeKeyMismatchErrType:
		return "RangeKeyMismatchErrType"
	case ReadWithinUncertaintyIntervalErrType:
		return "ReadWithinUncertaintyIntervalErrType"
	case TransactionAbortedErrType:
		return "TransactionAbortedErrType"
	case TransactionPushErrType:
		return "TransactionPushErrType"
	case TransactionRetryErrType:
		return "TransactionRetryErrType"
	case TransactionStatusErrType:
		return "TransactionStatusErrType"
	case LockConflictErrType:
		return "LockConflictErrType"
	case WriteTooOldErrType:
		return "WriteTooOldErrType"
	case OpRequiresTxnErrType:
		return "OpRequiresTxnErrType"
	case ConditionFailedErrType:
		return "ConditionFailedErrType"
	case LeaseRejectedErrType:
		return "LeaseRejectedErrType"
	case NodeUnavailableErrType:
		return "NodeUnavailableErrType"
	case RaftGroupDeletedErrType:
		return "RaftGroupDeletedErrType"
	case ReplicaCorruptionErrType:
		return "ReplicaCorruptionErrType"
	case ReplicaTooOldErrType:
		return "ReplicaTooOldErrType"
	case AmbiguousResultErrType:
		return "AmbiguousResultErrType"
	case StoreNotFoundErrType:
		return "StoreNotFoundErrType"
	case TransactionRetryWithProtoRefreshErrType:
		return "TransactionRetryWithProtoRefreshErrType"
	case IntegerOverflowErrType:
		return "IntegerOverflowErrType"
	case UnsupportedRequestErrType:
		return "UnsupportedRequestErrType"
	case BatchTimestampBeforeGCErrType:
		return "BatchTimestampBeforeGCErrType"
	case TxnAlreadyEncounteredErrType:
		return "TxnAlreadyEncounteredErrType"
	case IntentMissingErrType:
		return "IntentMissingErrType"
	case MergeInProgressErrType:
		return "MergeInProgressErrType"
	case RangeFeedRetryErrType:
		return "RangeFeedRetryErrType"
	case IndeterminateCommitErrType:
		return "IndeterminateCommitErrType"
	case InvalidLeaseErrType:
		return "InvalidLeaseErrType"
	case OptimisticEvalConflictsErrType:
		return "OptimisticEvalConflictsErrType"
	case MinTimestampBoundUnsatisfiableErrType:
		return "MinTimestampBoundUnsatisfiableErrType"
	case RefreshFailedErrType:
		return "RefreshFailedErrType"
	case MVCCHistoryMutationErrType:
		return "MVCCHistoryMutationErrType"
	case CommunicationErrType:
		return "CommunicationErrType"
	case InternalErrType:
		return "InternalErrType"
	default:
		return "ErrorDetailType(" + strconv.FormatInt(int64(i), 10) + ")"
	}
}
