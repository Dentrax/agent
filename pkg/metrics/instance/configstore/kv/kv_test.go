package kv

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cortexproject/cortex/pkg/ring/kv/codec"
	"github.com/grafana/agent/pkg/metrics/instance/configstore/kv/consul"
	"github.com/grafana/agent/pkg/metrics/instance/configstore/kv/etcd"
	"github.com/grafana/agent/pkg/metrics/instance/configstore/kv/pair"
)

func withFixtures(t *testing.T, f func(*testing.T, Client)) {
	for _, fixture := range []struct {
		name    string
		factory func() (Client, io.Closer, error)
	}{
		{"consul", func() (Client, io.Closer, error) {
			return consul.NewInMemoryClient(codec.String{}), etcd.NopCloser, nil
		}},
		{"etcd", func() (Client, io.Closer, error) {
			return etcd.Mock(codec.String{})
		}},
		{"prefixed/etcd", func() (cli Client, closer io.Closer, err error) {
			cli, closer, err = etcd.Mock(codec.String{})
			if err != nil {
				return
			}
			cli = PrefixClient(cli, "prefix/")
			return
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			client, closer, err := fixture.factory()
			require.NoError(t, err)
			defer closer.Close()
			f(t, client)
		})
	}
}

var (
	ctx = context.Background()
	key = "/key"
)

func TestCAS(t *testing.T) {
	withFixtures(t, func(t *testing.T, client Client) {
		// Blindly set key to "0".
		err := client.CAS(ctx, key, func(in interface{}) (interface{}, bool, error) {
			return "0", true, nil
		})
		require.NoError(t, err)

		// Swap key to i+1 iff its i.
		for i := 0; i < 10; i++ {
			err = client.CAS(ctx, key, func(in interface{}) (interface{}, bool, error) {
				require.EqualValues(t, strconv.Itoa(i), in)
				return strconv.Itoa(i + 1), true, nil
			})
			require.NoError(t, err)
		}

		// Make sure the CASes left the right value - "10".
		value, err := client.Get(ctx, key)
		require.NoError(t, err)
		require.EqualValues(t, "10", value)
	})
}

// TestNilCAS ensures we can return nil from the CAS callback when we don't
// want to modify the value.
func TestNilCAS(t *testing.T) {
	withFixtures(t, func(t *testing.T, client Client) {
		// Blindly set key to "0".
		err := client.CAS(ctx, key, func(in interface{}) (interface{}, bool, error) {
			return "0", true, nil
		})
		require.NoError(t, err)

		// Ensure key is "0" and don't set it.
		err = client.CAS(ctx, key, func(in interface{}) (interface{}, bool, error) {
			require.EqualValues(t, "0", in)
			return nil, false, nil
		})
		require.NoError(t, err)

		// Make sure value is still 0.
		value, err := client.Get(ctx, key)
		require.NoError(t, err)
		require.EqualValues(t, "0", value)
	})
}

func TestWatchKey(t *testing.T) {
	const key = "test"
	const max = 100
	const sleep = 50 * time.Millisecond
	const totalTestTimeout = 3 * max * sleep
	const expectedFactor = 0.75 // we may not see every single value

	withFixtures(t, func(t *testing.T, client Client) {
		observedValuesCh := make(chan string, max)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			// Start watching before we even start generating values.
			// Values will be buffered in the channel.
			client.WatchKey(ctx, key, func(value interface{}) bool {
				observedValuesCh <- value.(string)
				return true
			})
		}()

		// update value for the key
		go func() {
			for i := 0; i < max; i++ {
				// Start with sleeping, so that watching client see empty KV store at the beginning.
				time.Sleep(sleep)

				err := client.CAS(ctx, key, func(in interface{}) (out interface{}, retry bool, err error) {
					return fmt.Sprintf("%d", i), true, nil
				})

				if ctx.Err() != nil {
					break
				}
				require.NoError(t, err)
			}
		}()

		lastObservedValue := -1
		observedCount := 0

		totalDeadline := time.After(totalTestTimeout)

		for watching := true; watching; {
			select {
			case <-totalDeadline:
				watching = false
			case valStr := <-observedValuesCh:
				val, err := strconv.Atoi(valStr)
				if err != nil {
					t.Fatal("Unexpected value observed:", valStr)
				}

				if val <= lastObservedValue {
					t.Fatal("Unexpected value observed:", val, "previous:", lastObservedValue)
				}
				lastObservedValue = val
				observedCount++

				if observedCount >= expectedFactor*max {
					watching = false
				}
			}
		}

		if observedCount < expectedFactor*max {
			t.Errorf("expected at least %.0f%% observed values, got %.0f%% (observed count: %d)", 100*expectedFactor, 100*float64(observedCount)/max, observedCount)
		}
	})
}

func TestWatchPrefix(t *testing.T) {
	withFixtures(t, func(t *testing.T, client Client) {
		const prefix = "test/"
		const prefix2 = "ignore/"

		// We are going to generate this number of updates, sleeping between each update.
		const max = 100
		const sleep = time.Millisecond * 10
		// etcd seems to be quite slow. If we finish faster, test will end sooner.
		// (We regularly see generators taking up to 5 seconds to produce all messages on some platforms!)
		const totalTestTimeout = 10 * time.Second

		observedKeysCh := make(chan string, max)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		wg := sync.WaitGroup{}

		wg.Add(1)
		go func() {
			defer wg.Done()

			// start watching before we even start generating values. values will be buffered
			client.WatchPrefix(ctx, prefix, func(key string, val interface{}) bool {
				observedKeysCh <- key
				return true
			})
		}()

		gen := func(p string) {
			defer wg.Done()

			start := time.Now()
			for i := 0; i < max && ctx.Err() == nil; i++ {
				// Start with sleeping, so that watching client can see empty KV store at the beginning.
				time.Sleep(sleep)

				key := fmt.Sprintf("%s%d", p, i)
				err := client.CAS(ctx, key, func(in interface{}) (out interface{}, retry bool, err error) {
					return key, true, nil
				})

				if ctx.Err() != nil {
					break
				}
				require.NoError(t, err)
			}
			t.Log("Generator finished in", time.Since(start))
		}

		wg.Add(2)
		go gen(prefix)
		go gen(prefix2) // we don't want to see these keys reported

		observedKeys := map[string]int{}

		totalDeadline := time.After(totalTestTimeout)

		start := time.Now()
		for watching := true; watching; {
			select {
			case <-totalDeadline:
				watching = false
			case key := <-observedKeysCh:
				observedKeys[key]++
				if len(observedKeys) == max {
					watching = false
				}
			}
		}
		t.Log("Watching finished in", time.Since(start))

		// Stop all goroutines and wait until terminated.
		cancel()
		wg.Wait()

		// verify that each key was reported once, and keys outside prefix were not reported
		for i := 0; i < max; i++ {
			key := fmt.Sprintf("%s%d", prefix, i)

			if observedKeys[key] != 1 {
				t.Errorf("key %s has incorrect value %d", key, observedKeys[key])
			}
			delete(observedKeys, key)
		}

		if len(observedKeys) > 0 {
			t.Errorf("unexpected keys reported: %v", observedKeys)
		}
	})
}

func TestWatchPrefix_Deletes(t *testing.T) {
	withFixtures(t, func(t *testing.T, client Client) {
		observedKVPs := make(chan pair.KVP, 1)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Watch for keys in the background. As soon as a key comes through, delete
		// it. The next invoke should be from the deleted key.
		go client.WatchPrefix(ctx, "", func(key string, val interface{}) bool {
			if val != nil {
				err := client.Delete(ctx, key)
				require.NoError(t, err)
				return true
			}

			observedKVPs <- pair.KVP{Key: key, Value: val}
			return false
		})

		// Wait before generating a key to be deleted.
		time.Sleep(250 * time.Millisecond)
		err := client.CAS(ctx, "key", func(in interface{}) (out interface{}, retry bool, err error) {
			return "value", false, nil
		})
		require.NoError(t, err)

		select {
		case <-time.After(5 * time.Second):
			require.FailNow(t, "test timed out waiting for delete event")
		case kvp := <-observedKVPs:
			require.Equal(t, "key", kvp.Key)
			require.Nil(t, kvp.Value, "value must be nil to indicate deleteion")
		}
	})
}

// TestList makes sure stored keys are listed back.
func TestList(t *testing.T) {
	kvpsToCreate := []pair.KVP{
		{Key: "a", Value: "value_a"},
		{Key: "b", Value: "value_b"},
		{Key: "c", Value: "value_c"},
	}

	withFixtures(t, func(t *testing.T, client Client) {
		for _, kvp := range kvpsToCreate {
			err := client.CAS(context.Background(), kvp.Key, func(in interface{}) (out interface{}, retry bool, err error) {
				return kvp.Value, false, nil
			})
			require.NoError(t, err)
		}

		storedKVPs, err := client.List(context.Background(), "")
		require.NoError(t, err)
		sort.Slice(storedKVPs, func(i, j int) bool { return storedKVPs[i].Key < storedKVPs[j].Key })

		require.Equal(t, kvpsToCreate, storedKVPs)
	})
}