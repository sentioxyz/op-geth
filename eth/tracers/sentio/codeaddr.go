package sentio

import (
	"github.com/ethereum/go-ethereum/common"
)

// codeAddrTracker derives, per call frame, the address the running code was
// loaded from (the pre-hook API's Contract.CodeAddr), using only OnEnter hook
// data — no EVM internals. The EVM passes the code-load address as OnEnter's
// `to` for every frame type: CALL/STATICCALL (code == storage == to),
// DELEGATECALL/CALLCODE (code = to, storage context stays at `from`),
// CREATE/CREATE2 (initcode at to).
//
// OnOpcode's depth is the OnEnter depth + 1 (the interpreter bumps evm.depth
// before stepping), so the executing frame's record lives at opDepth-1.
// Frames that never execute opcodes (precompiles, empty code, calls failing
// before interpretation) still fire OnEnter; their slots are overwritten by
// the next frame at the same depth and never read.
type codeAddrTracker struct {
	byDepth []common.Address
}

// onEnter records the code address for the frame entered at depth.
func (c *codeAddrTracker) onEnter(depth int, codeAddr common.Address) {
	if depth >= len(c.byDepth) {
		grown := make([]common.Address, depth+1)
		copy(grown, c.byDepth)
		c.byDepth = grown
	}
	c.byDepth[depth] = codeAddr
}

// codeAddress returns the code address for the frame executing at the given
// OnOpcode depth, falling back to the given address (the frame's storage
// address) if the frame was somehow never recorded.
func (c *codeAddrTracker) codeAddress(opDepth int, fallback common.Address) common.Address {
	if i := opDepth - 1; i >= 0 && i < len(c.byDepth) {
		return c.byDepth[i]
	}
	return fallback
}
