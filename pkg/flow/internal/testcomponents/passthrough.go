package testcomponents

import (
	"context"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/grafana/agent/component"
)

func init() {
	component.Register(component.Registration{
		Name:    "testcomponents.passthrough",
		Args:    PassthroughConfig{},
		Exports: PassthroughExports{},

		Build: func(opts component.Options, args component.Arguments) (component.Component, error) {
			return NewPassthrough(opts, args.(PassthroughConfig))
		},
	})
}

// PassthroughConfig configures the testcomponents.passthrough component.
type PassthroughConfig struct {
	Input string `hcl:"input,attr"`
}

// PassthroughExports describes exported fields for the
// testcomponents.passthrough component.
type PassthroughExports struct {
	Output string `hcl:"output,optional"`
}

// Passthrough implements the testcomponents.passthrough component, where it
// always emits its input as an output.
type Passthrough struct {
	opts component.Options
	log  log.Logger
}

// NewPassthrough creates a new passthrough component.
func NewPassthrough(o component.Options, cfg PassthroughConfig) (*Passthrough, error) {
	t := &Passthrough{opts: o, log: o.Logger}
	if err := t.Update(cfg); err != nil {
		return nil, err
	}
	return t, nil
}

var (
	_ component.Component      = (*Passthrough)(nil)
	_ component.DebugComponent = (*Passthrough)(nil)
)

// Run implements Component.
func (t *Passthrough) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Update implements Component.
func (t *Passthrough) Update(args component.Arguments) error {
	c := args.(PassthroughConfig)

	level.Info(t.log).Log("msg", "passing through value", "value", c.Input)
	t.opts.OnStateChange(PassthroughExports{Output: c.Input})
	return nil
}

// DebugInfo implements DebugComponent.
func (t *Passthrough) DebugInfo() interface{} {
	// Useless, but for demonstration purposes shows how to export debug
	// information. Real components would want to use something interesting here
	// which allow the user to investigate issues of the internal state of a
	// component.
	return passthroughDebugInfo{
		ComponentVersion: "v0.1-beta.0",
	}
}

type passthroughDebugInfo struct {
	ComponentVersion string `hcl:"component_version"`
}
