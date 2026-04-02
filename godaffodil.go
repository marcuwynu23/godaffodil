package godaffodil

import "github.com/marcuwynu23/godaffodil/internal"

type Config = internal.Config
type Step = internal.Step
type Daffodil = internal.Daffodil
type WatchOptions = internal.WatchOptions
type Watcher = internal.Watcher
type InventoryTarget = internal.InventoryTarget

func New(cfg Config) (*Daffodil, error) {
	return internal.New(cfg)
}
