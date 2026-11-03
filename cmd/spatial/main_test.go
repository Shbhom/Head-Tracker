//go:build darwin && cgo

package main

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestExpectedStop(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want bool
	}{
		{context.Canceled, true}, {context.DeadlineExceeded, true},
		{errors.Join(context.Canceled, io.ErrClosedPipe), false},
		{errors.Join(context.DeadlineExceeded, errors.New("cleanup failed")), false},
		{io.EOF, false},
	} {
		if got := expectedStop(tt.err); got != tt.want {
			t.Errorf("expectedStop(%v)=%v", tt.err, got)
		}
	}
}
