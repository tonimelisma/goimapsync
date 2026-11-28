package state_test

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tonimelisma/pimvault/internal/state"
)

// lockHolderEnv makes the test binary act as another pimvault run that
// takes the lock, says so and waits to be killed.
const lockHolderEnv = "PIMVAULT_TEST_LOCK_HOLDER"

func TestMain(m *testing.M) {
	if dir := os.Getenv(lockHolderEnv); dir != "" {
		holdLock(dir)
	}

	os.Exit(m.Run())
}

// holdLock takes the lock and keeps it until the process is killed.
func holdLock(dir string) {
	unlock, err := state.Lock(dir)
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}

	// A collected file is closed, which would drop the lock: the pending
	// defer keeps it referenced.
	defer runtime.KeepAlive(unlock)

	fmt.Println("locked")

	select {} // held until the test kills this process
}

// Validates: PV-110
func TestLock_SecondRunIsRefusedAndCrashedRunsLockIsTakenOver(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")

	holder := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$") //nolint:gosec // G204: the test binary itself.
	holder.Env = append(os.Environ(), lockHolderEnv+"="+dir)
	out, err := holder.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, holder.Start())

	line, err := bufio.NewReader(out).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "locked\n", line)

	_, err = state.Lock(dir)
	require.ErrorIs(t, err, state.ErrLocked)
	assert.Contains(t, err.Error(), "process "+strconv.Itoa(holder.Process.Pid))

	// The other run crashes: its lock is taken over.
	require.NoError(t, holder.Process.Kill())
	_ = holder.Wait() //nolint:errcheck // killed on purpose.

	unlock, err := state.Lock(dir)
	require.NoError(t, err)
	require.NoError(t, unlock())
}

func TestLock_ReleasedLockCanBeTakenAgain(t *testing.T) {
	dir := t.TempDir()

	unlock, err := state.Lock(dir)
	require.NoError(t, err)

	_, err = state.Lock(dir)
	require.ErrorIs(t, err, state.ErrLocked, "a second lock in the same process is refused too")

	require.NoError(t, unlock())

	unlock, err = state.Lock(dir)
	require.NoError(t, err)
	require.NoError(t, unlock())
}
