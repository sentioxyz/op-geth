// Copyright 2022 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package tracetest

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/eth/tracers"
	"github.com/ethereum/go-ethereum/tests"

	// Force-load the sentio tracers
	_ "github.com/ethereum/go-ethereum/eth/tracers/sentio"
)

// sentioPrestateAccount is the subset of the sentioPrestateTracer account
// output this test asserts on.
type sentioPrestateAccount struct {
	Storage map[common.Hash]common.Hash `json:"storage"`
}

type sentioPrestateDiff struct {
	Pre  map[common.Address]sentioPrestateAccount `json:"pre"`
	Post map[common.Address]sentioPrestateAccount `json:"post"`
}

// TestSentioPrestateTracerStorageAttribution replays the upstream
// prestate-tracer diff-mode fixtures through sentioPrestateTracer and checks
// that storage diffs land on the same accounts and slots that the upstream
// prestateTracer reports. The two tracers prune empty accounts differently,
// so only storage-bearing accounts are compared.
//
// Regression test for the hook-API port bug where scope.Caller() was used as
// the storage owner: storage was attributed to the parent frame (so the real
// diffs were dropped from the result) and frames whose caller was never
// looked up panicked, surfacing as "method handler crashed" at the RPC layer.
func TestSentioPrestateTracerStorageAttribution(t *testing.T) {
	files, err := os.ReadDir(filepath.Join("testdata", "prestate_tracer_with_diff_mode"))
	if err != nil {
		t.Fatalf("failed to retrieve tracer test suite: %v", err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		// sentioPrestateTracer doesn't implement disableCode/disableStorage,
		// so fixtures exercising those flags expect different storage sets.
		if strings.Contains(file.Name(), "disable") {
			continue
		}
		t.Run(camel(strings.TrimSuffix(file.Name(), ".json")), func(t *testing.T) {
			t.Parallel()

			var (
				test = new(prestateTracerTest)
				tx   = new(types.Transaction)
			)
			if blob, err := os.ReadFile(filepath.Join("testdata", "prestate_tracer_with_diff_mode", file.Name())); err != nil {
				t.Fatalf("failed to read testcase: %v", err)
			} else if err := json.Unmarshal(blob, test); err != nil {
				t.Fatalf("failed to parse testcase: %v", err)
			}
			if err := tx.UnmarshalBinary(common.FromHex(test.Input)); err != nil {
				t.Fatalf("failed to parse testcase input: %v", err)
			}
			var (
				signer  = types.MakeSigner(test.Genesis.Config, new(big.Int).SetUint64(uint64(test.Context.Number)), uint64(test.Context.Time))
				state   = tests.MakePreState(rawdb.NewMemoryDatabase(), test.Genesis.Alloc, false, rawdb.HashScheme)
				context = test.Context.toBlockContext(test.Genesis, state.StateDB)
			)
			defer state.Close()

			tracer, err := tracers.DefaultDirectory.New("sentioPrestateTracer", new(tracers.Context), test.TracerConfig, test.Genesis.Config)
			if err != nil {
				t.Fatalf("failed to create sentio prestate tracer: %v", err)
			}

			msg, err := core.TransactionToMessage(tx, signer, context.BaseFee)
			if err != nil {
				t.Fatalf("failed to prepare transaction for tracing: %v", err)
			}
			evm := vm.NewEVM(context, state.StateDB, test.Genesis.Config, vm.Config{Tracer: tracer.Hooks})
			tracer.OnTxStart(evm.GetVMContext(), tx, msg.From)
			vmRet, err := core.ApplyMessage(evm, msg, new(core.GasPool).AddGas(tx.Gas()))
			if err != nil {
				t.Fatalf("failed to execute transaction: %v", err)
			}
			tracer.OnTxEnd(&types.Receipt{GasUsed: vmRet.UsedGas}, nil)
			res, err := tracer.GetResult()
			if err != nil {
				t.Fatalf("failed to retrieve trace result: %v", err)
			}

			var got sentioPrestateDiff
			if err := json.Unmarshal(res, &got); err != nil {
				t.Fatalf("failed to parse sentio trace result: %v", err)
			}
			wantBlob, err := json.Marshal(test.Result)
			if err != nil {
				t.Fatalf("failed to marshal expected result: %v", err)
			}
			var want sentioPrestateDiff
			if err := json.Unmarshal(wantBlob, &want); err != nil {
				t.Fatalf("failed to parse expected result: %v", err)
			}

			if have, expect := storageKeySets(got.Pre), storageKeySets(want.Pre); have != expect {
				t.Errorf("pre storage attribution mismatch\n have: %v\n want: %v\n", have, expect)
			}
			if have, expect := storageKeySets(got.Post), storageKeySets(want.Post); have != expect {
				t.Errorf("post storage attribution mismatch\n have: %v\n want: %v\n", have, expect)
			}
		})
	}
}

// storageKeySets renders the storage-bearing accounts of a diff side as a
// deterministic "addr:[slot slot ...]" string for comparison.
func storageKeySets(side map[common.Address]sentioPrestateAccount) string {
	var entries []string
	for addr, acc := range side {
		if len(acc.Storage) == 0 {
			continue
		}
		var slots []string
		for slot := range acc.Storage {
			slots = append(slots, slot.Hex())
		}
		sort.Strings(slots)
		entries = append(entries, fmt.Sprintf("%s:%v", addr.Hex(), slots))
	}
	sort.Strings(entries)
	return strings.Join(entries, " ")
}
