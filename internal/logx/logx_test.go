package logx_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/FelipeFuhr/ffreis-presentation-compiler/internal/logx"
)

func TestNewRespectsVerbosity(t *testing.T) {
	t.Run("quiet drops debug lines", func(t *testing.T) {
		var buf bytes.Buffer
		logx.New(&buf, false).Debug("hidden")
		if buf.Len() != 0 {
			t.Errorf("debug line leaked at info level: %q", buf.String())
		}
	})

	t.Run("verbose keeps debug lines", func(t *testing.T) {
		var buf bytes.Buffer
		logx.New(&buf, true).Debug("shown")
		if !strings.Contains(buf.String(), "shown") {
			t.Errorf("debug line missing at debug level: %q", buf.String())
		}
	})

	t.Run("info is always emitted", func(t *testing.T) {
		var buf bytes.Buffer
		logx.New(&buf, false).Info("always")
		if !strings.Contains(buf.String(), "always") {
			t.Errorf("info line missing: %q", buf.String())
		}
	})
}
