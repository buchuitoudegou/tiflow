// Copyright 2022 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package sinkmanager

import (
	"context"

	"github.com/pingcap/errors"
	"github.com/pingcap/log"
	"github.com/pingcap/tiflow/cdc/model"
	"github.com/pingcap/tiflow/cdc/processor/sourcemanager"
	"github.com/pingcap/tiflow/cdc/processor/sourcemanager/engine"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

type sinkWorker struct {
	changefeedID  model.ChangeFeedID
	sourceManager *sourcemanager.SourceManager
	memQuota      *memQuota
	eventCache    *redoEventCache
	redoEnabled   bool
	// splitTxn indicates whether to split the transaction into multiple batches.
	splitTxn bool
	// enableOldValue indicates whether to enable the old value feature.
	// If it is enabled, we need to deal with the compatibility of the data format.
	enableOldValue bool

	metricRedoEventCacheHit  prometheus.Counter
	metricRedoEventCacheMiss prometheus.Counter
}

// newWorker creates a new worker.
func newSinkWorker(
	changefeedID model.ChangeFeedID,
	sourceManager *sourcemanager.SourceManager,
	quota *memQuota,
	eventCache *redoEventCache,
	redoEnabled bool,
	splitTxn bool,
	enableOldValue bool,
) *sinkWorker {
	return &sinkWorker{
		changefeedID:   changefeedID,
		sourceManager:  sourceManager,
		memQuota:       quota,
		eventCache:     eventCache,
		redoEnabled:    redoEnabled,
		splitTxn:       splitTxn,
		enableOldValue: enableOldValue,

		metricRedoEventCacheHit:  RedoEventCacheAccess.WithLabelValues(changefeedID.Namespace, changefeedID.ID, "hit"),
		metricRedoEventCacheMiss: RedoEventCacheAccess.WithLabelValues(changefeedID.Namespace, changefeedID.ID, "miss"),
	}
}

func (w *sinkWorker) handleTasks(ctx context.Context, taskChan <-chan *sinkTask) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case task := <-taskChan:
			err := w.handleTask(ctx, task)
			if err != nil {
				return err
			}
		}
	}
}

func (w *sinkWorker) handleTask(ctx context.Context, task *sinkTask) (finalErr error) {
	lowerBound := task.lowerBound
	upperBound := task.getUpperBound(task.tableSink)
	if !upperBound.IsCommitFence() {
		log.Panic("sink task upperbound must be a ResolvedTs",
			zap.String("namespace", w.changefeedID.Namespace),
			zap.String("changefeed", w.changefeedID.ID),
			zap.Int64("tableID", task.tableID),
			zap.Any("upperBound", upperBound))
	}

	// First time to run the task, we have initialized memory quota for the table.
	availableMem := requestMemSize
	usedMem := uint64(0)

	// Used to record the last written position.
	// We need to use it to update the lower bound of the table sink.
	var lastPos engine.Position

	// To advance table sink in different cases: splitTxn is true or not.
	committedTxnSize := uint64(0) // Can be used in `advanceTableSink`.
	pendingTxnSize := uint64(0)   // Can be used in `advanceTableSinkWithBatchID`.
	lastTxnCommitTs := uint64(0)  // Can be used in `advanceTableSink`
	currTxnCommitTs := uint64(0)  // Can be used in `advanceTableSinkWithBatchID`.
	events := make([]*model.RowChangedEvent, 0, 1024)
	batchID := uint64(1)

	if w.redoEnabled && w.eventCache != nil {
		drained, err := w.fetchFromCache(task, &lowerBound, &upperBound, &batchID)
		if err != nil {
			return errors.Trace(err)
		}
		if drained {
			w.memQuota.refund(availableMem - usedMem)
			log.Debug("MemoryQuotaTracing: refund memory for table sink task",
				zap.String("namespace", w.changefeedID.Namespace),
				zap.String("changefeed", w.changefeedID.ID),
				zap.Int64("tableID", task.tableID),
				zap.Uint64("memory", availableMem-usedMem))
			task.callback(lowerBound.Prev())
			return nil
		}
	}

	needEmitAndAdvance := func() bool {
		// For splitTxn is enabled or not, sizes of events can be advanced will be different.
		return (w.splitTxn && committedTxnSize+pendingTxnSize >= maxUpdateIntervalSize) ||
			(!w.splitTxn && committedTxnSize >= maxUpdateIntervalSize)
	}
	doEmitAndAdvance := func(isLastTime bool) (err error) {
		if len(events) > 0 {
			task.tableSink.appendRowChangedEvents(events...)
			events = events[:0]
			if cap(events) > 1024 {
				events = make([]*model.RowChangedEvent, 0, 1024)
			}
		}
		log.Debug("check should advance or not",
			zap.String("namespace", w.changefeedID.Namespace),
			zap.String("changefeed", w.changefeedID.ID),
			zap.Int64("tableID", task.tableID),
			zap.Bool("splitTxn", w.splitTxn),
			zap.Uint64("currTxnCommitTs", currTxnCommitTs),
			zap.Uint64("lastTxnCommitTs", lastTxnCommitTs),
			zap.Bool("isLastTime", isLastTime))
		if w.splitTxn {
			if currTxnCommitTs == 0 {
				return
			}
			if currTxnCommitTs == lastPos.CommitTs && lastPos.IsCommitFence() {
				// All transactions before currTxnCommitTs are resolved.
				err = w.advanceTableSink(task, currTxnCommitTs, committedTxnSize+pendingTxnSize)
			} else {
				// This will advance some complete transactions before currTxnCommitTs,
				// and one partail transaction with `batchID`.
				err = w.advanceTableSinkWithBatchID(task, currTxnCommitTs, committedTxnSize+pendingTxnSize, batchID)
				batchID += 1
			}
			committedTxnSize = 0
			pendingTxnSize = 0
		} else {
			if lastTxnCommitTs == 0 {
				return
			}
			err = w.advanceTableSink(task, lastTxnCommitTs, committedTxnSize)
			committedTxnSize = 0
			// It's the last time we call `doEmitAndAdvance`, but `pendingTxnSize`
			// hasn't been recorded yet. To avoid losing it, record it manually.
			if isLastTime && pendingTxnSize > 0 {
				w.memQuota.record(task.tableID, model.NewResolvedTs(currTxnCommitTs), pendingTxnSize)
			}
		}
		return
	}

	maybeEmitAndAdvance := func(allFinished, txnFinished bool) error {
		memoryHighUsage := availableMem < usedMem

		// Do emit in such situations:
		// 1. we use more memory than we required;
		// 2. the pending batch size exceeds maxUpdateIntervalSize;
		// 3. all events are received.
		if memoryHighUsage || allFinished || needEmitAndAdvance() {
			if err := doEmitAndAdvance(false); err != nil {
				return errors.Trace(err)
			}
		}

		if memoryHighUsage {
			w.memQuota.forceAcquire(usedMem - availableMem)
			log.Debug("MemoryQuotaTracing: force acquire memory for table sink task",
				zap.String("namespace", w.changefeedID.Namespace),
				zap.String("changefeed", w.changefeedID.ID),
				zap.Int64("tableID", task.tableID),
				zap.Uint64("memory", usedMem-availableMem))
			availableMem = usedMem
		}

		if allFinished {
			// There is no more events and some required memory isn't used.
			if availableMem > usedMem {
				w.memQuota.refund(availableMem - usedMem)
				log.Debug("MemoryQuotaTracing: refund memory for table sink task",
					zap.String("namespace", w.changefeedID.Namespace),
					zap.String("changefeed", w.changefeedID.ID),
					zap.Int64("tableID", task.tableID),
					zap.Uint64("memory", availableMem-usedMem))
			}
		} else if usedMem >= availableMem {
			if txnFinished {
				if w.memQuota.tryAcquire(requestMemSize) {
					availableMem += requestMemSize
					log.Debug("MemoryQuotaTracing: try acquire memory for table sink task",
						zap.String("namespace", w.changefeedID.Namespace),
						zap.String("changefeed", w.changefeedID.ID),
						zap.Int64("tableID", task.tableID),
						zap.Uint64("memory", requestMemSize))
				}
			} else {
				if !w.splitTxn {
					w.memQuota.forceAcquire(requestMemSize)
					availableMem += requestMemSize
					log.Debug("MemoryQuotaTracing: force acquire memory for table sink task",
						zap.String("namespace", w.changefeedID.Namespace),
						zap.String("changefeed", w.changefeedID.ID),
						zap.Int64("tableID", task.tableID),
						zap.Uint64("memory", requestMemSize))
				} else {
					// NOTE: if splitTxn is true it's not required to force acquire memory.
					if err := w.memQuota.blockAcquire(requestMemSize); err != nil {
						return errors.Trace(err)
					}
					availableMem += requestMemSize
					log.Debug("MemoryQuotaTracing: block acquire memory for table sink task",
						zap.String("namespace", w.changefeedID.Namespace),
						zap.String("changefeed", w.changefeedID.ID),
						zap.Int64("tableID", task.tableID),
						zap.Uint64("memory", requestMemSize))
				}
			}
		}
		return nil
	}

	// lowerBound and upperBound are both closed intervals.
	allEventSize := uint64(0)
	allEventCount := 0
	iter := w.sourceManager.FetchByTable(task.tableID, lowerBound, upperBound)
	defer func() {
		w.metricRedoEventCacheMiss.Add(float64(allEventSize))
		task.tableSink.receivedEventCount.Add(int64(allEventCount))
		if !w.redoEnabled {
			task.tableSink.updateReceivedSorterCommitTs(currTxnCommitTs)
			eventCount := rangeEventCount{pos: lastPos, events: allEventCount}
			task.tableSink.updateRangeEventCounts(eventCount)
		}

		if err := iter.Close(); err != nil {
			log.Error("Sink worker fails to close iterator",
				zap.String("namespace", w.changefeedID.Namespace),
				zap.String("changefeed", w.changefeedID.ID),
				zap.Int64("tableID", task.tableID),
				zap.Error(err))
		}

		log.Debug("Sink task finished",
			zap.String("namespace", w.changefeedID.Namespace),
			zap.String("changefeed", w.changefeedID.ID),
			zap.Int64("tableID", task.tableID),
			zap.Any("lowerBound", lowerBound),
			zap.Any("upperBound", upperBound),
			zap.Bool("splitTxn", w.splitTxn),
			zap.Any("lastPos", lastPos))

		if finalErr == nil {
			// Otherwise we can't ensure all events before `lastPos` are emitted.
			task.callback(lastPos)
		}
	}()

	for availableMem > usedMem && !task.isCanceled() {
		e, pos, err := iter.Next(ctx)
		if err != nil {
			return errors.Trace(err)
		}
		// There is no more data. It means that we finish this scan task.
		if e == nil {
			lastPos = upperBound
			currTxnCommitTs = upperBound.CommitTs
			lastTxnCommitTs = upperBound.CommitTs
			committedTxnSize += pendingTxnSize
			pendingTxnSize = 0
			return maybeEmitAndAdvance(true, true)
		}
		allEventCount += 1

		if pos.Valid() {
			lastPos = pos
		}
		if currTxnCommitTs != e.CRTs {
			lastTxnCommitTs = currTxnCommitTs
			currTxnCommitTs = e.CRTs
			committedTxnSize += pendingTxnSize
			pendingTxnSize = 0
			batchID = 1
		}
		if pos.IsCommitFence() {
			lastTxnCommitTs = currTxnCommitTs
			committedTxnSize += pendingTxnSize
			pendingTxnSize = 0
		}

		// NOTICE: The event can be filtered by the event filter.
		if e.Row != nil {
			// For all rows, we add table replicate ts, so mysql sink can determine safe-mode.
			e.Row.ReplicatingTs = task.tableSink.replicateTs
			x, size, err := convertRowChangedEvents(w.changefeedID, task.tableID, w.enableOldValue, e)
			if err != nil {
				return err
			}
			events = append(events, x...)
			allEventSize += size
			usedMem += size
			if pos.IsCommitFence() {
				committedTxnSize += size
			} else {
				pendingTxnSize += size
			}
		}

		if err := maybeEmitAndAdvance(false, pos.Valid()); err != nil {
			return errors.Trace(err)
		}
	}
	return doEmitAndAdvance(true)
}

func (w *sinkWorker) fetchFromCache(
	task *sinkTask, // task is read-only here.
	lowerBound *engine.Position,
	upperBound *engine.Position,
	batchID *uint64,
) (cacheDrained bool, err error) {
	newLowerBound := *lowerBound
	newUpperBound := *upperBound

	cache := w.eventCache.getAppender(task.tableID)
	if cache == nil {
		return
	}
	popRes := cache.pop(*lowerBound, *upperBound)
	if popRes.success {
		if len(popRes.events) > 0 {
			task.tableSink.receivedEventCount.Add(int64(popRes.pushCount))
			w.metricRedoEventCacheHit.Add(float64(popRes.size))
			task.tableSink.appendRowChangedEvents(popRes.events...)
		}

		var resolvedTs model.ResolvedTs
		isCommitFence := popRes.boundary.IsCommitFence()
		if w.splitTxn {
			resolvedTs = model.NewResolvedTs(popRes.boundary.CommitTs)
			if !isCommitFence {
				resolvedTs.Mode = model.BatchResolvedMode
				resolvedTs.BatchID = *batchID
				*batchID += 1
			}
		} else {
			if isCommitFence {
				resolvedTs = model.NewResolvedTs(popRes.boundary.CommitTs)
			} else {
				resolvedTs = model.NewResolvedTs(popRes.boundary.CommitTs - 1)
			}
		}
		// NOTE: the recorded size can be not accurate, but let it be.
		w.memQuota.record(task.tableID, resolvedTs, popRes.releaseSize)
		if err = task.tableSink.updateResolvedTs(resolvedTs); err == nil {
			newLowerBound = popRes.boundary.Next()
		}
	} else {
		newUpperBound = popRes.boundary.Prev()
	}
	cacheDrained = newLowerBound.Compare(newUpperBound) > 0
	log.Debug("fetchFromCache is performed",
		zap.String("namespace", w.changefeedID.Namespace),
		zap.String("changefeed", w.changefeedID.ID),
		zap.Int64("tableID", task.tableID),
		zap.Bool("success", popRes.success),
		zap.Bool("cacheDrained", cacheDrained),
		zap.Any("lowerBound", lowerBound),
		zap.Any("upperBound", upperBound),
		zap.Any("newLowerBound", newLowerBound),
		zap.Any("newUpperBound", newUpperBound),
	)
	*lowerBound = newLowerBound
	*upperBound = newUpperBound
	return
}

func (w *sinkWorker) advanceTableSinkWithBatchID(t *sinkTask, commitTs model.Ts, size uint64, batchID uint64) error {
	log.Debug("Advance table sink with batch ID",
		zap.String("namespace", w.changefeedID.Namespace),
		zap.String("changefeed", w.changefeedID.ID),
		zap.Int64("tableID", t.tableID),
		zap.Uint64("commitTs", commitTs),
		zap.Uint64("batchID", batchID),
	)

	resolvedTs := model.NewResolvedTs(commitTs)
	resolvedTs.Mode = model.BatchResolvedMode
	resolvedTs.BatchID = batchID
	if size > 0 {
		w.memQuota.record(t.tableID, resolvedTs, size)
	}
	return t.tableSink.updateResolvedTs(resolvedTs)
}

func (w *sinkWorker) advanceTableSink(t *sinkTask, commitTs model.Ts, size uint64) error {
	log.Debug("Advance table sink without batch ID",
		zap.String("namespace", w.changefeedID.Namespace),
		zap.String("changefeed", w.changefeedID.ID),
		zap.Int64("tableID", t.tableID),
		zap.Uint64("commitTs", commitTs),
	)

	resolvedTs := model.NewResolvedTs(commitTs)
	if size > 0 {
		w.memQuota.record(t.tableID, resolvedTs, size)
	}
	return t.tableSink.updateResolvedTs(resolvedTs)
}
