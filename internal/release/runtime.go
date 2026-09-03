// Package release wires the workflow engine to concrete optional executors.
// Keeping this composition root separate prevents the workflow core from
// importing Jenkins or any future delivery platform.
package release

import (
	"sync"

	"ares/internal/db"
	"ares/internal/executor/jenkinsstep"
	"ares/internal/workflow"
)

type Runtime struct {
	Registry    *workflow.Registry
	Store       *workflow.XORMStore
	Service     *workflow.Service
	Coordinator *workflow.Coordinator
}

var (
	runtimeOnce sync.Once
	shared      *Runtime
)

// Shared returns the process-wide workflow composition. Database
// initialization must complete before the first call in the application.
func Shared() *Runtime {
	runtimeOnce.Do(func() {
		registry := workflow.DefaultRegistry()
		if err := registry.Register(jenkinsstep.New()); err != nil {
			panic(err)
		}
		store := workflow.NewXORMStore(db.Engine)
		shared = &Runtime{
			Registry:    registry,
			Store:       store,
			Service:     workflow.NewService(store, registry),
			Coordinator: workflow.NewCoordinator(store, registry),
		}
	})
	return shared
}
