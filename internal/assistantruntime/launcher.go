package assistantruntime

import (
	"context"
	"sync"
	"sync/atomic"
)

// FakeLauncher provides the same provider-neutral process boundary a real
// child launcher would provide, without starting a subprocess.
type FakeLauncher struct {
	Config FakeConfig

	mu      sync.Mutex
	nextPID int
	active  *fakeProcess
}

var _ Launcher = (*FakeLauncher)(nil)

func NewFakeLauncher(config ...FakeConfig) *FakeLauncher {
	launcher := &FakeLauncher{}
	if len(config) > 0 {
		launcher.Config = config[0]
	}
	return launcher
}

func (l *FakeLauncher) Start(ctx context.Context, spec LaunchSpec) (Client, Process, error) {
	if err := contextErr(ctx); err != nil {
		return nil, nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active != nil && l.active.helper.State() == StateReady {
		return nil, nil, ErrAlreadyStarted
	}
	cfg := l.Config
	if spec.AssistantAddress != "" {
		cfg.AssistantAddress = spec.AssistantAddress
	}
	if spec.RuntimeRevision != "" {
		cfg.RuntimeRevision = spec.RuntimeRevision
	}
	if spec.CapabilityRevision != "" {
		cfg.CapabilityRevision = spec.CapabilityRevision
	}
	helper := NewFakeHelper(cfg)
	if err := helper.Start(ctx); err != nil {
		return nil, nil, err
	}
	l.nextPID++
	if l.nextPID == 0 {
		l.nextPID = 1
	}
	process := &fakeProcess{helper: helper, pid: l.nextPID, done: make(chan struct{})}
	helper.setCrashHook(process.signalCrash)
	l.active = process
	return helper, process, nil
}

type fakeProcess struct {
	helper *FakeHelper
	pid    int
	done   chan struct{}
	once   sync.Once
	dead   atomic.Bool
}

var _ Process = (*fakeProcess)(nil)

func (p *fakeProcess) PID() int { return p.pid }

func (p *fakeProcess) Wait() error {
	<-p.done
	return nil
}

func (p *fakeProcess) signalCrash() {
	p.dead.Store(true)
	p.once.Do(func() { close(p.done) })
}

func (p *fakeProcess) Stop(ctx context.Context) error {
	if err := p.helper.Stop(ctx); err != nil {
		return err
	}
	p.dead.Store(true)
	p.once.Do(func() { close(p.done) })
	return nil
}
