package contract

import (
	"errors"
	"fmt"
	"testing"
)

func TestWSSeamErrBackpressure(t *testing.T) {
	if !errors.Is(fmt.Errorf("send hint: %w", ErrWSBackpressure), ErrWSBackpressure) {
		t.Fatal("wrapped backpressure must remain recognizable by the hub")
	}
}
