package workflow

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sync"
	"sync/atomic"
	"time"
)

var ErrWorkerDrainTimeout = errors.New("worker 排空超时")

// WorkerOptions is a validated, immutable worker configuration snapshot.
// Owner is only an injection seam for deterministic tests and embedders;
// production startup leaves it empty so every process receives a CSPRNG ID.
type WorkerOptions struct {
	Owner              string
	Concurrency        int
	ClaimBatchSize     int
	ScanInterval       time.Duration
	LeaseDuration      time.Duration
	RenewInterval      time.Duration
	NormalPollInterval time.Duration
	BackoffMin         time.Duration
	BackoffMax         time.Duration
	DrainTimeout       time.Duration
	MaxTransitions     int
	Jitter             func(time.Duration) time.Duration
}

type Worker struct {
	store       WorkerStore
	coordinator *Coordinator
	options     WorkerOptions
	owner       string
	started     atomic.Bool
}

type activeWorkerTask struct {
	cancel context.CancelCauseFunc
}

type shutdownDeadlineProvider interface {
	ShutdownDeadline() (time.Time, bool)
}

func NewWorker(store WorkerStore, coordinator *Coordinator, options WorkerOptions) (*Worker, error) {
	if store == nil {
		return nil, fmt.Errorf("worker store 不能为空")
	}
	if coordinator == nil {
		return nil, fmt.Errorf("worker coordinator 不能为空")
	}
	applyWorkerOptionDefaults(&options)
	if err := validateWorkerOptions(options); err != nil {
		return nil, err
	}
	owner := options.Owner
	if owner == "" {
		generated, err := generateWorkerOwner()
		if err != nil {
			return nil, fmt.Errorf("生成 worker owner: %w", err)
		}
		owner = generated
	}
	if !validLeaseOwner(owner) {
		return nil, fmt.Errorf("worker owner 无效")
	}
	return &Worker{store: store, coordinator: coordinator, options: options, owner: owner}, nil
}

func applyWorkerOptionDefaults(options *WorkerOptions) {
	if options.Concurrency == 0 {
		options.Concurrency = 8
	}
	if options.ClaimBatchSize == 0 {
		options.ClaimBatchSize = options.Concurrency
	}
	if options.ScanInterval == 0 {
		options.ScanInterval = time.Second
	}
	if options.LeaseDuration == 0 {
		options.LeaseDuration = 30 * time.Second
	}
	if options.RenewInterval == 0 {
		options.RenewInterval = 10 * time.Second
	}
	if options.NormalPollInterval == 0 {
		options.NormalPollInterval = 5 * time.Second
	}
	if options.BackoffMin == 0 {
		options.BackoffMin = 2 * time.Second
	}
	if options.BackoffMax == 0 {
		options.BackoffMax = 2 * time.Minute
	}
	if options.DrainTimeout == 0 {
		options.DrainTimeout = 20 * time.Second
	}
	if options.MaxTransitions == 0 {
		options.MaxTransitions = 101
	}
	if options.Jitter == nil {
		options.Jitter = cryptoDurationJitter
	}
}

func validateWorkerOptions(options WorkerOptions) error {
	if options.Concurrency < 1 || options.Concurrency > maxTaskLeaseBatch {
		return fmt.Errorf("worker 并发数必须在 1 到 %d 之间", maxTaskLeaseBatch)
	}
	if options.ClaimBatchSize < 1 || options.ClaimBatchSize > options.Concurrency {
		return fmt.Errorf("worker 单批领取数必须在 1 到并发数之间")
	}
	checks := []struct {
		name  string
		value time.Duration
	}{
		{"扫描间隔", options.ScanInterval},
		{"任务租期", options.LeaseDuration},
		{"续租间隔", options.RenewInterval},
		{"普通轮询间隔", options.NormalPollInterval},
		{"退避下界", options.BackoffMin},
		{"退避上界", options.BackoffMax},
		{"排空超时", options.DrainTimeout},
	}
	for _, check := range checks {
		if check.value <= 0 || check.value > maxWorkerDelay {
			return fmt.Errorf("worker %s超出允许范围", check.name)
		}
	}
	if options.RenewInterval > options.LeaseDuration/3 {
		return fmt.Errorf("worker 续租间隔不能超过租期的三分之一")
	}
	if options.BackoffMin > options.BackoffMax {
		return fmt.Errorf("worker 退避下界不能超过上界")
	}
	if options.MaxTransitions < 1 || options.MaxTransitions > 10000 {
		return fmt.Errorf("worker 单轮状态转换上限无效")
	}
	return nil
}

func generateWorkerOwner() (string, error) {
	random := make([]byte, 16)
	if _, err := cryptorand.Read(random); err != nil {
		return "", err
	}
	return "ares-" + hex.EncodeToString(random), nil
}

func cryptoDurationJitter(upperBound time.Duration) time.Duration {
	if upperBound <= 0 {
		return 0
	}
	value, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(upperBound)+1))
	if err != nil {
		return upperBound / 2
	}
	return time.Duration(value.Int64())
}

// Run owns the complete lifecycle of the v2 task worker. It is intentionally
// single-use: starting the same process identity twice would violate the
// one-renewer-per-task invariant.
func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.store == nil || w.coordinator == nil {
		return fmt.Errorf("worker 未初始化")
	}
	if ctx == nil {
		return fmt.Errorf("worker context 不能为空")
	}
	if !w.started.CompareAndSwap(false, true) {
		return fmt.Errorf("worker 只能启动一次")
	}

	var activeMu sync.Mutex
	active := make(map[int]activeWorkerTask, w.options.Concurrency)
	wake := make(chan struct{}, 1)
	var claimFailures uint32
	var shutdownDeadline time.Time
	resolveShutdownDeadline := func() time.Time {
		if !shutdownDeadline.IsZero() {
			return shutdownDeadline
		}
		if provider, ok := ctx.(shutdownDeadlineProvider); ok {
			if deadline, present := provider.ShutdownDeadline(); present {
				shutdownDeadline = deadline
				return shutdownDeadline
			}
		}
		shutdownDeadline = time.Now().Add(w.options.DrainTimeout)
		return shutdownDeadline
	}

	activeCount := func() int {
		activeMu.Lock()
		defer activeMu.Unlock()
		return len(active)
	}
	launch := func(lease TaskLease) bool {
		taskCtx, cancelTask := context.WithCancelCause(context.Background())
		activeMu.Lock()
		if _, duplicate := active[lease.TaskID]; duplicate {
			activeMu.Unlock()
			cancelTask(ErrLeaseLost)
			return false
		}
		active[lease.TaskID] = activeWorkerTask{cancel: cancelTask}
		activeMu.Unlock()
		go func() {
			defer func() {
				cancelTask(context.Canceled)
				activeMu.Lock()
				delete(active, lease.TaskID)
				activeMu.Unlock()
				select {
				case wake <- struct{}{}:
				default:
				}
			}()
			w.runTaskLease(taskCtx, cancelTask, lease)
		}()
		return true
	}

	for ctx.Err() == nil {
		freeSlots := w.options.Concurrency - activeCount()
		if freeSlots <= 0 {
			select {
			case <-ctx.Done():
			case <-wake:
			}
			continue
		}
		claimLimit := min(freeSlots, w.options.ClaimBatchSize)
		leases, err := w.store.AcquireTaskLeases(ctx, w.owner, claimLimit, w.options.LeaseDuration)
		delay := time.Duration(0)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			if claimFailures < MaxPollFailureCount {
				claimFailures++
			}
			if claimFailures == 1 || claimFailures == MaxPollFailureCount || claimFailures&(claimFailures-1) == 0 {
				slog.Warn("领取工作流任务失败", "error_type", fmt.Sprintf("%T", err))
			}
			delay = w.claimFailureDelay(claimFailures - 1)
		} else if ctx.Err() != nil {
			w.releaseUnstartedLeases(leases, resolveShutdownDeadline())
			break
		} else {
			claimFailures = 0
			launched := 0
			for _, lease := range leases {
				if launched >= freeSlots {
					w.releaseUnstartedLeases([]TaskLease{lease}, time.Time{})
					continue
				}
				if launch(lease) {
					launched++
				} else {
					w.releaseUnstartedLeases([]TaskLease{lease}, time.Time{})
				}
			}
		}

		if delay <= 0 {
			delay = boundedDurationAdd(w.options.ScanInterval, w.jitter(w.options.ScanInterval/5))
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}

	if activeCount() == 0 {
		return nil
	}
	remaining := time.Until(resolveShutdownDeadline())
	if remaining <= 0 {
		w.cancelActiveTasks(active, &activeMu)
		return ErrWorkerDrainTimeout
	}
	drainTimer := time.NewTimer(remaining)
	defer drainTimer.Stop()
	for activeCount() > 0 {
		select {
		case <-wake:
		case <-drainTimer.C:
			w.cancelActiveTasks(active, &activeMu)
			return ErrWorkerDrainTimeout
		}
	}
	return nil
}

func (w *Worker) cancelActiveTasks(active map[int]activeWorkerTask, activeMu *sync.Mutex) {
	activeMu.Lock()
	cancellations := make([]context.CancelCauseFunc, 0, len(active))
	for _, task := range active {
		cancellations = append(cancellations, task.cancel)
	}
	activeMu.Unlock()
	for _, cancelTask := range cancellations {
		cancelTask(ErrWorkerDrainTimeout)
	}
}

func (w *Worker) runTaskLease(taskCtx context.Context, cancelTask context.CancelCauseFunc, lease TaskLease) {
	renewCtx, stopRenewal := context.WithCancel(taskCtx)
	renewalDone := make(chan struct{})
	go func() {
		defer close(renewalDone)
		w.renewTaskLease(renewCtx, cancelTask, lease)
	}()

	result, runErr := w.coordinator.RunUntilBlocked(taskCtx, lease, w.options.MaxTransitions)
	stopRenewal()
	<-renewalDone
	if runErr == nil && result.Terminal {
		return
	}
	if context.Cause(taskCtx) != nil || errors.Is(runErr, ErrLeaseLost) {
		return
	}

	pollFailed := runErr != nil || result.PollBackoff
	delay := w.normalPollDelay()
	if pollFailed {
		delay = w.failureBackoffDelay(lease.FailureCount)
	}
	releaseCtx, cancelRelease := context.WithTimeout(taskCtx, w.options.RenewInterval)
	err := w.store.ReleaseTaskLease(releaseCtx, lease, delay, pollFailed)
	cancelRelease()
	if err != nil && !errors.Is(err, ErrLeaseLost) && taskCtx.Err() == nil {
		slog.Warn("释放工作流任务租约失败", "task_id", lease.TaskID, "error_type", fmt.Sprintf("%T", err))
	}
}

func (w *Worker) renewTaskLease(ctx context.Context, cancelTask context.CancelCauseFunc, lease TaskLease) {
	ticker := time.NewTicker(w.options.RenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewCtx, cancelRenew := context.WithTimeout(ctx, w.options.RenewInterval)
			err := w.store.RenewTaskLease(renewCtx, lease, w.options.LeaseDuration)
			cancelRenew()
			if err != nil {
				if ctx.Err() == nil {
					slog.Warn("工作流任务续租失败", "task_id", lease.TaskID, "error_type", fmt.Sprintf("%T", err))
					cancelTask(err)
				}
				return
			}
		}
	}
}

func (w *Worker) releaseUnstartedLeases(leases []TaskLease, shutdownDeadline time.Time) {
	timeout := min(w.options.RenewInterval, w.options.DrainTimeout/4)
	if timeout <= 0 {
		timeout = time.Millisecond
	}
	if timeout > 2*time.Second {
		timeout = 2 * time.Second
	}
	releaseDeadline := time.Now().Add(timeout)
	if !shutdownDeadline.IsZero() && shutdownDeadline.Before(releaseDeadline) {
		releaseDeadline = shutdownDeadline
	}
	if !releaseDeadline.After(time.Now()) {
		return
	}
	releaseCtx, cancelRelease := context.WithDeadline(context.Background(), releaseDeadline)
	defer cancelRelease()
	for _, lease := range leases {
		err := w.store.ReleaseTaskLease(releaseCtx, lease, 0, false)
		if err != nil && !errors.Is(err, ErrLeaseLost) {
			slog.Warn("释放未启动的工作流任务租约失败", "task_id", lease.TaskID, "error_type", fmt.Sprintf("%T", err))
		}
	}
}

func (w *Worker) normalPollDelay() time.Duration {
	return boundedDurationAdd(w.options.NormalPollInterval, w.jitter(w.options.NormalPollInterval/5))
}

func (w *Worker) failureBackoffDelay(previousFailures uint32) time.Duration {
	window := w.options.BackoffMin
	for remaining := min(previousFailures, uint32(MaxPollFailureCount)); remaining > 0 && window < w.options.BackoffMax; remaining-- {
		if window > w.options.BackoffMax/2 {
			window = w.options.BackoffMax
			break
		}
		window *= 2
	}
	if window > w.options.BackoffMax {
		window = w.options.BackoffMax
	}
	return w.jitter(window)
}

func (w *Worker) claimFailureDelay(previousFailures uint32) time.Duration {
	delay := w.failureBackoffDelay(previousFailures)
	// Full jitter may legally return zero. A database outage must still wait at
	// least one scan interval so many replicas cannot turn a rare zero sample
	// into a synchronized request/log storm.
	if delay < w.options.ScanInterval {
		return w.options.ScanInterval
	}
	return delay
}

func (w *Worker) jitter(upperBound time.Duration) time.Duration {
	if upperBound <= 0 {
		return 0
	}
	value := w.options.Jitter(upperBound)
	if value < 0 {
		return 0
	}
	if value > upperBound {
		return upperBound
	}
	return value
}

func boundedDurationAdd(left, right time.Duration) time.Duration {
	if right <= 0 {
		return left
	}
	if left >= maxWorkerDelay-right {
		return maxWorkerDelay
	}
	return left + right
}
