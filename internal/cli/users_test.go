package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"vlessvmorectl/internal/store"
)

const pw = "hunter2hunter2"

// run drives the real command tree, so flag wiring and arg validation are covered too.
func run(t *testing.T, dir string, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer

	root := NewRootCmd()
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append(args, "--data-dir", dir))

	err = root.Execute()
	return out.String(), errBuf.String(), err
}

func openStore(t *testing.T, dir string) *store.Admins {
	t.Helper()
	a, err := store.OpenAdmins(filepath.Join(dir, store.AdminsFile))
	if err != nil {
		t.Fatalf("OpenAdmins: %v", err)
	}
	return a
}

func TestUsersAdd(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, err := run(t, dir, "", "users", "add", "alice", pw)
	if err != nil {
		t.Fatalf("users add: %v (%s)", err, stderr)
	}
	if !strings.Contains(stdout, "created administrator alice") {
		t.Errorf("stdout: %q", stdout)
	}
	// Printed on every mutation so a `docker run` without -v, where the file lands in
	// the container's ephemeral layer and vanishes, is visible rather than silent.
	if !strings.Contains(stdout, "wrote ") {
		t.Errorf("stdout does not name the file it wrote: %q", stdout)
	}

	if _, err := openStore(t, dir).Verify("alice", pw); err != nil {
		t.Errorf("the created password does not verify: %v", err)
	}
}

// TestUsersAddWarnsAboutArgvPassword — and on stderr, so `users add … 2>/dev/null`
// still scripts cleanly.
func TestUsersAddWarnsAboutArgvPassword(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, err := run(t, dir, "", "users", "add", "alice", pw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "shell history") {
		t.Errorf("stderr does not warn about the password being in argv: %q", stderr)
	}
	if strings.Contains(stdout, "shell history") {
		t.Error("the warning went to stdout, which would pollute a script's output")
	}
}

func TestUsersAddPasswordStdin(t *testing.T) {
	dir := t.TempDir()

	if _, stderr, err := run(t, dir, pw+"\n", "users", "add", "alice", "--password-stdin"); err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if _, err := openStore(t, dir).Verify("alice", pw); err != nil {
		t.Errorf("the password from stdin does not verify: %v", err)
	}

	// No argv warning on this path — nothing was exposed.
	_, stderr, _ := run(t, dir, pw+"\n", "users", "add", "bob", "--password-stdin")
	if strings.Contains(stderr, "shell history") {
		t.Errorf("--password-stdin warned anyway: %q", stderr)
	}
}

func TestUsersAddRejectsBothPasswordSources(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := run(t, dir, pw, "users", "add", "alice", pw, "--password-stdin"); err == nil {
		t.Error("want an error when the password is given twice")
	}
}

func TestUsersAddDuplicate(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := run(t, dir, "", "users", "add", "alice", pw); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, dir, "", "users", "add", "alice", pw); err == nil {
		t.Error("want a conflict error on the second add")
	}
}

func TestUsersAddShortPassword(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := run(t, dir, "", "users", "add", "alice", "short"); err == nil {
		t.Error("want an error for a password under the minimum length")
	}
}

// TestUsersListJSONHasNoHash guards the obvious future mistake: reaching for the
// storage struct instead of the projection when adding a field to --json.
func TestUsersListJSONHasNoHash(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := run(t, dir, "", "users", "add", "alice", pw); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := run(t, dir, "", "users", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "password_hash") {
		t.Errorf("--json exposed the hash field:\n%s", stdout)
	}
	if strings.Contains(stdout, "$2a$") || strings.Contains(stdout, "$2b$") {
		t.Errorf("--json exposed a bcrypt hash:\n%s", stdout)
	}
	if !strings.Contains(stdout, "alice") {
		t.Errorf("--json did not list the administrator:\n%s", stdout)
	}
}

func TestUsersListEmpty(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, err := run(t, dir, "", "users", "ls")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "no administrators yet") {
		t.Errorf("stderr should hint how to create one: %q", stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout should be empty for an empty list: %q", stdout)
	}
}

// TestUsersRemoveRefusesTheLastAdmin: emptying the list locks everyone out of a running
// panel, and the only way back in is a shell on the host.
func TestUsersRemoveRefusesTheLastAdmin(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := run(t, dir, "", "users", "add", "alice", pw); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, dir, "", "users", "rm", "alice", "-y")
	if err == nil {
		t.Fatal("want a refusal without --force")
	}
	if !strings.Contains(err.Error(), "last administrator") {
		t.Errorf("error should explain why: %v", err)
	}
	if openStore(t, dir).Count() != 1 {
		t.Error("the administrator was removed anyway")
	}

	if _, _, err := run(t, dir, "", "users", "rm", "alice", "-y", "--force"); err != nil {
		t.Fatalf("--force should allow it: %v", err)
	}
	if openStore(t, dir).Count() != 0 {
		t.Error("--force did not remove the administrator")
	}
}

func TestUsersRemoveConfirmation(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alice", "bob"} {
		if _, _, err := run(t, dir, "", "users", "add", name, pw); err != nil {
			t.Fatal(err)
		}
	}

	// A non-interactive stdin answers no, so a piped command cannot accidentally agree
	// to a deletion.
	if _, stderr, err := run(t, dir, "", "users", "rm", "bob"); err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if openStore(t, dir).Count() != 2 {
		t.Error("bob was removed without confirmation")
	}

	if _, _, err := run(t, dir, "y\n", "users", "rm", "bob"); err != nil {
		t.Fatal(err)
	}
	if openStore(t, dir).Count() != 1 {
		t.Error("an explicit yes did not remove bob")
	}
}

func TestUsersRemoveUnknown(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := run(t, dir, "", "users", "add", "alice", pw); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, dir, "", "users", "rm", "nobody", "-y"); err == nil {
		t.Error("want an error for an unknown administrator")
	}
}

func TestUsersPasswd(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := run(t, dir, "", "users", "add", "alice", pw); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := run(t, dir, "", "users", "passwd", "alice", "brandnewpassword")
	if err != nil {
		t.Fatalf("%v (%s)", err, stderr)
	}
	if !strings.Contains(stdout, "changed the password") {
		t.Errorf("stdout: %q", stdout)
	}
	// Say it, because otherwise nobody believes it.
	if !strings.Contains(stderr, "signed out of the panel everywhere") {
		t.Errorf("stderr does not mention session invalidation: %q", stderr)
	}

	a := openStore(t, dir)
	if _, err := a.Verify("alice", "brandnewpassword"); err != nil {
		t.Errorf("the new password does not verify: %v", err)
	}
	if _, err := a.Verify("alice", pw); err == nil {
		t.Error("the old password still works")
	}
}

// TestUsersAliasAndArgvDispatch: the image symlinks `users` to the binary so
// `docker exec vlessvmorectl users add alice` works, and `user` is accepted as an alias
// matching the sibling's spelling.
func TestUsersAliasAndArgvDispatch(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := run(t, dir, "", "user", "add", "alice", pw); err != nil {
		t.Fatalf("the `user` alias does not work: %v", err)
	}
	if openStore(t, dir).Count() != 1 {
		t.Error("the alias did not create the administrator")
	}
}

func TestVersionCommand(t *testing.T) {
	// Not via run(), which appends --data-dir; version takes no flags.
	var out bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(out.String(), "vlessvmorectl") {
		t.Errorf("stdout: %q", out.String())
	}
}

// TestNoCommandLeaksAHash sweeps every read command's output.
func TestNoCommandLeaksAHash(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := run(t, dir, "", "users", "add", "alice", pw); err != nil {
		t.Fatal(err)
	}
	hash := func() string {
		a, _ := openStore(t, dir).Get("alice")
		return a.PasswordHash
	}()

	for _, args := range [][]string{
		{"users", "ls"},
		{"users", "ls", "--json"},
	} {
		stdout, stderr, err := run(t, dir, "", args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.Contains(stdout+stderr, hash) {
			t.Errorf("%v leaked the stored hash", args)
		}
		if strings.Contains(stdout+stderr, pw) {
			t.Errorf("%v leaked the password", args)
		}
	}
}
