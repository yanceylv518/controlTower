package tuning

import (
	"context"
	"crypto/rand"
	"fmt"
	"hash/fnv"
	"log"
	"sync"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/channelupdates"
)

// Routine probes use the direct control connection, never an Agent heartbeat
// queue. A queue cannot honour a ten-second start schedule.
type RoutineProber interface {
	channelcontrol.Prober
	Check(context.Context) error
}
type routineProbeProvider interface {
	RoutineProbeController(string) (RoutineProber, string, error)
}

type routineOutcome int

const (
	routineUnknown routineOutcome = iota
	routineHealthy
	routineError
	routineSlow
	routineCanceled
)

type routineStreak struct{ slow, errors, healthy int }

func (s *routineStreak) add(o routineOutcome, policy ...ContinuousDispatchParams) bool {
	switch o {
	case routineHealthy:
		s.slow, s.errors = 0, 0
		s.healthy++
	case routineSlow:
		s.slow++
		s.healthy = 0
	case routineError:
		s.errors++
		// A channel error does not prove latency recovered. Only a healthy
		// result clears known slow evidence; unknown coverage still breaks it.
		s.healthy = 0
	default:
		*s = routineStreak{}
	}
	return s.slow >= optionalRules(policy).ProbeSlowLimit || s.errors >= optionalRules(policy).RoutineErrorLimit
}

type routineResult struct {
	id        string
	sequence  int
	outcome   routineOutcome
	started   time.Time
	duration  float64
	scheduled bool
}
type routineTiming struct{ interval, timeout time.Duration }

type routineRuntime struct {
	initialDelay time.Duration
	dispatch     func(routineDispatch)
	settled      func(routineResult)
}

// Stable per-channel phase avoids a startup burst without changing cadence.
func routineInitialDelay(site string, channel int64, interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	h := fnv.New64a()
	fmt.Fprintf(h, "%s/%d", site, channel)
	return time.Duration(h.Sum64() % uint64(interval))
}

// A settled contiguous fault segment is sufficient evidence even while an
// earlier request is pending. Gaps and successful/unknown results break it.
func confirmedRoutineFault(pending map[int]routineResult, first, end int, prefix routineStreak, policy ContinuousDispatchParams) (routineStreak, time.Time, bool) {
	streak := prefix
	for sequence := first; sequence < end; sequence++ {
		r, settled := pending[sequence]
		if !settled {
			streak = routineStreak{}
			continue
		}
		if streak.add(r.outcome, policy) {
			return streak, r.started, true
		}
	}
	return routineStreak{}, time.Time{}, false
}

// Order evidence by request start, not completion. Each request settles at its
// deadline even if a transport ignores cancellation; its late return is ignored.
// Slots remain occupied until actual IO exits, so ignored cancellations cannot
// create unbounded requests. Missed starts are gaps, never channel failures.
func runRoutineChannel(ctx context.Context, p RoutineProber, channel int64, model string, slots chan struct{}, timing routineTiming, consume func(routineStreak, time.Time) bool, record ...func(routineResult)) {
	runRoutineChannelWithPolicy(ctx, p, channel, model, slots, timing, DefaultPolicy().Continuous, consume, record...)
}

func runRoutineChannelWithPolicy(ctx context.Context, p RoutineProber, channel int64, model string, slots chan struct{}, timing routineTiming, policy ContinuousDispatchParams, consume func(routineStreak, time.Time) bool, record ...func(routineResult)) {
	runRoutineChannelRuntime(ctx, p, channel, model, slots, timing, policy, routineRuntime{}, consume, record...)
}

func runRoutineChannelRuntime(ctx context.Context, p RoutineProber, channel int64, model string, slots chan struct{}, timing routineTiming, policy ContinuousDispatchParams, runtime routineRuntime, consume func(routineStreak, time.Time) bool, record ...func(routineResult)) {
	if !policy.RoutineProbeEnabledFor(channel) {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	var observations sync.WaitGroup
	defer func() { cancel(); observations.Wait() }()
	results := make(chan routineResult, max(8, int(timing.timeout/timing.interval)+4))
	maxInFlight := max(4, int((timing.timeout+timing.interval-1)/timing.interval)+1)
	// Keep room for a complete fault segment behind pending IO, including
	// operator-configured thresholds larger than the physical concurrency.
	maxPending := maxInFlight + policy.Rules().ProbeSlowLimit + policy.Rules().RoutineErrorLimit
	localSlots := make(chan struct{}, maxInFlight)
	nextSequence, folded := 0, 0
	pending := map[int]routineResult{}
	streak := routineStreak{}
	next := time.NewTimer(runtime.initialDelay)
	defer next.Stop()
	reset := func() {
		if !next.Stop() {
			select {
			case <-next.C:
			default:
			}
		}
		next.Reset(timing.interval)
	}
	lastPendingGap := -1
	launch := func(scheduled bool) {
		if ctx.Err() != nil {
			return
		}
		started := time.Now()
		id := rand.Text()
		report := func(reason string) {
			if runtime.dispatch != nil {
				runtime.dispatch(routineDispatch{id: id, at: started, scheduled: scheduled, skipReason: reason})
			}
		}
		// Bound unresolved sequence evidence as well as actual HTTP requests.
		// Fast failures behind a slow earlier request must not form a retry storm.
		if nextSequence-folded >= maxPending {
			report(probeSkipEvidence)
			// One bounded gap prevents evidence on either side of missed starts
			// being joined after capacity becomes available again.
			if lastPendingGap != nextSequence-1 {
				pending[nextSequence] = routineResult{sequence: nextSequence, outcome: routineUnknown, started: started}
				lastPendingGap = nextSequence
				nextSequence++
			}
			return
		}
		sequence := nextSequence
		nextSequence++
		acquired := false
		skipReason := probeSkipChannel
		select {
		case localSlots <- struct{}{}:
			select {
			case slots <- struct{}{}:
				acquired = true
			default:
				<-localSlots
				skipReason = probeSkipGlobal
			}
		default:
		}
		if !acquired {
			report(skipReason)
			pending[sequence] = routineResult{sequence: sequence, outcome: routineUnknown, started: started}
			return
		}
		report("")
		observations.Add(1)
		go func() {
			defer observations.Done()
			observed := routineResult{id: id, sequence: sequence, started: started, scheduled: scheduled, outcome: routineCanceled}
			defer func() {
				if observed.outcome == routineCanceled {
					observed.duration = min(time.Since(started).Seconds(), timing.timeout.Seconds())
				}
				if runtime.settled != nil {
					runtime.settled(observed)
				}
			}()
			requestCtx, cancel := context.WithDeadline(ctx, started.Add(timing.timeout))
			defer cancel()
			returned := make(chan routineResult, 1)
			go func() {
				defer func() { <-slots; <-localSlots }()
				result, err := channelcontrol.ProbeWithBudget(requestCtx, p, channel, model, timing.timeout)
				outcome := routineUnknown
				switch {
				case result.Slow || result.Duration > timing.timeout.Seconds():
					outcome = routineSlow
				case err == nil && result.Success:
					outcome = routineHealthy
				case err == nil:
					outcome = routineError
				}
				returned <- routineResult{outcome: outcome, duration: time.Since(started).Seconds()}
			}()
			var outcome routineOutcome
			duration := timing.timeout.Seconds()
			select {
			case <-ctx.Done():
				return
			case <-requestCtx.Done():
				outcome = routineSlow
			case measured := <-returned:
				outcome, duration = measured.outcome, measured.duration
				if duration >= timing.timeout.Seconds() {
					outcome = routineSlow
				}
			}
			if ctx.Err() != nil {
				return
			}
			observed.outcome, observed.duration = outcome, min(duration, timing.timeout.Seconds())
			if outcome == routineSlow {
				observed.duration = timing.timeout.Seconds()
			}
			select {
			case results <- observed:
			case <-ctx.Done():
			}
		}()
	}
	for {
		accelerate := false
		select {
		case <-ctx.Done():
			return
		case <-next.C:
			launch(true)
			reset()
		case r := <-results:
			if r.outcome == routineSlow {
				r.duration = timing.timeout.Seconds()
			}
			for _, save := range record {
				save(r)
			}
			pending[r.sequence] = r
			// The first error triggers a replacement without waiting for the regular
			// interval. Further settled errors may confirm quickly, bounded by slots.
			accelerate = r.outcome == routineError
		}
		if fault, at, tripped := confirmedRoutineFault(pending, folded, nextSequence, streak, policy); tripped && !consume(fault, at) {
			return
		}
		for {
			r, ok := pending[folded]
			if !ok {
				break
			}
			delete(pending, folded)
			folded++
			streak.add(r.outcome, policy)
		}
		if accelerate {
			launch(false)
			reset()
		}
	}
}

type routineRun struct {
	signature string
	cancel    context.CancelFunc
	done      chan struct{}
}

func routineEligible(base ChannelBaseValue, state ContinuousState, policy PolicyRecord) bool {
	return policy.Policy.Continuous.RoutineProbeEnabledFor(base.ChannelID) && base.BaseWeight > 0 && len(base.Models) == 1 && policy.Policy.DispatchModes[base.ModelName] == "auto" &&
		state.PausedReason != ManualDisabled && !state.CircuitDisabled && !state.RoutineCircuitLatched && (state.Phase == "" || state.Phase == "normal" || state.Phase == "soft_start")
}

func (e *Engine) runRoutineProbes(ctx context.Context) {
	provider, ok := e.store.(routineProbeProvider)
	if !ok {
		return
	}
	cs, ok := e.store.(ContinuousStore)
	if !ok {
		return
	}
	running := map[channelKey]routineRun{}
	slots := make(chan struct{}, 1024)
	var workers sync.WaitGroup
	defer func() {
		for _, r := range running {
			r.cancel()
		}
		workers.Wait()
	}()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		wanted := map[channelKey]bool{}
		sites, err := e.store.ListEnabledSites()
		if err != nil {
			log.Printf("routine probes: site inventory unavailable")
		}
		for _, site := range sites {
			if ctx.Err() != nil {
				return
			}
			policy, found, err := e.store.GetPolicy(site)
			if err != nil || !found || !policy.Policy.Continuous.Rules().RoutineProbeEnabled {
				continue
			}
			bases, err := cs.ListChannelBaseValues(site, "")
			if err != nil {
				continue
			}
			states, err := cs.ListContinuousStates(site)
			if err != nil {
				continue
			}
			byID := map[int64]ContinuousState{}
			for _, s := range states {
				byID[s.ChannelID] = s
			}
			prober, config, err := provider.RoutineProbeController(site)
			if err != nil || prober == nil {
				continue
			}
			for _, base := range bases {
				state := byID[base.ChannelID]
				if !routineEligible(base, state, policy) {
					continue
				}
				key := channelKey{site, base.ChannelID}
				signature := fmt.Sprintf("%s/%s/%s/%s/%d", config, base.ModelName, base.UpdatedAt.Format(time.RFC3339Nano), policy.UpdatedAt.Format(time.RFC3339Nano), state.ControlRevision)
				wanted[key] = true
				if old, exists := running[key]; exists {
					select {
					case <-old.done:
						delete(running, key)
					default:
						if old.signature != signature {
							old.cancel()
						}
						continue
					}
				}
				runCtx, cancel := context.WithCancel(ctx)
				done := make(chan struct{})
				running[key] = routineRun{signature, cancel, done}
				workers.Add(1)
				go func() {
					defer workers.Done()
					defer close(done)
					timing := routineTiming{time.Duration(policy.Policy.Continuous.Rules().RoutineIntervalSeconds) * time.Second, time.Duration(policy.Policy.Continuous.Rules().ProbeTimeoutSeconds) * time.Second}
					runtime := routineRuntime{initialDelay: routineInitialDelay(key.site, key.channel, timing.interval), dispatch: func(d routineDispatch) {
						e.probeCoverage.record(key, d)
						v := probeRecord(base, key.site, d.id, d.at, d.scheduled, timing.timeout)
						if d.skipReason != "" {
							at := d.at.UTC()
							v.Outcome = "skipped"
							v.Reason = d.skipReason
							v.FinishedAt = &at
						}
						e.probeRecords.put(v)
					}, settled: func(r routineResult) {
						e.probeRecords.put(probeRecordResult(probeRecord(base, key.site, r.id, r.started, r.scheduled, timing.timeout), r))
					}}
					runRoutineChannelRuntime(runCtx, prober, key.channel, base.ModelName, slots, timing, policy.Policy.Continuous, runtime, func(streak routineStreak, started time.Time) bool {
						return e.applyRoutineEvidence(runCtx, prober, key, base, policy, state.ControlRevision, streak, started)
					}, func(r routineResult) {
						if !r.scheduled || (r.outcome != routineHealthy && r.outcome != routineSlow) {
							return
						}
						sample := RoutineProbeSample{InstanceID: key.site, ChannelID: key.channel, ModelName: base.ModelName, ControlRevision: state.ControlRevision, BaseUpdatedAt: base.UpdatedAt, StartedAt: r.started, DurationSeconds: r.duration}
						select {
						case e.routineSamples <- sample:
						default:
							log.Printf("routine probe speed queue full site=%s channel=%d", key.site, key.channel)
						}
					})
				}()
			}
		}
		for key, r := range running {
			if !wanted[key] {
				r.cancel()
				delete(running, key)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (e *Engine) applyRoutineEvidence(ctx context.Context, prober RoutineProber, key channelKey, observed ChannelBaseValue, policy PolicyRecord, revision int64, streak routineStreak, started time.Time) bool {
	cs := e.store.(ContinuousStore)
	tripped := streak.slow >= policy.Policy.Continuous.Rules().ProbeSlowLimit || streak.errors >= policy.Policy.Continuous.Rules().RoutineErrorLimit
	if !tripped {
		return true
	}
	// A shared admin/network outage must not be attributed to every channel.
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := prober.Check(checkCtx)
	cancel()
	if err != nil {
		log.Printf("routine probes: control unavailable site=%s channel=%d", key.site, key.channel)
		return false
	}
	guard := e.guard(key.site, key.channel)
	guard.Lock()
	defer guard.Unlock()
	if ctx.Err() != nil {
		return false
	}
	latest, found, err := e.store.GetPolicy(key.site)
	if err != nil || !found || !latest.UpdatedAt.Equal(policy.UpdatedAt) {
		return false
	}
	bases, err := cs.ListChannelBaseValues(key.site, "")
	if err != nil {
		return false
	}
	var base ChannelBaseValue
	for _, b := range bases {
		if b.ChannelID == key.channel {
			base = b
			break
		}
	}
	if !base.UpdatedAt.Equal(observed.UpdatedAt) || base.ModelName != observed.ModelName {
		return false
	}
	states, err := cs.ListContinuousStates(key.site)
	if err != nil {
		return false
	}
	var state ContinuousState
	for _, s := range states {
		if s.ChannelID == key.channel {
			state = s
			break
		}
	}
	if state.ControlRevision != revision || !routineEligible(base, state, latest) {
		return false
	}
	now := time.Now().UTC()
	state.InstanceID, state.ChannelID, state.ModelName = key.site, key.channel, base.ModelName
	state.Evaluation = &EvaluationContext{BaseWeight: base.BaseWeight, BaseUpdatedAt: base.UpdatedAt, PolicyUpdatedAt: latest.UpdatedAt, EvaluatedAt: now, Params: latest.Policy.Continuous}
	e.settleCapacityWrite(cs, key.site, base, &state, now)
	// Supersede a prior ordinary decision before persisting the fault latch.
	// Execution gates reject that old positive-weight decision afterwards.
	state.Capacity.PendingCommandID = ""
	state.RoutineCircuitLatched = true
	state.Phase, state.Multiplier, state.ProposedWeight = "circuit", 0, 0
	state.CircuitDisabled, state.CircuitStatusTarget = true, 2
	state.CircuitOpenedAt = &now
	state.ProbeCommandID = nil
	state.ProbeAttempts, state.ProbeSuccesses, state.ProbeSlowStreak, state.ProbeDurationSum = 0, 0, 0, 0
	state.SoftStartPending = false
	if err = cs.PutContinuousState(state); err != nil {
		return false
	}
	rec := continuousEvent(key.site, base, state, "routine_probe_circuit", "auto", now)
	rec.Evidence["slow_streak"], rec.Evidence["error_streak"] = streak.slow, streak.errors
	rec.Evidence["probe_started_at"] = started
	_ = e.store.InsertRecommendation(rec)
	e.advanceCircuitStatus(cs, key.site, base, &state, latest.Policy.Continuous, now)

	state.UpdatedAt = now
	if err = cs.PutContinuousState(state); err != nil {
		log.Printf("routine probe state persistence failed site=%s channel=%d", key.site, key.channel)
	}
	guard.version = e.version.Add(1)
	channelupdates.Notify(key.site)
	return false
}
