package switcher

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/zalando/go-keyring"
)

type Provider struct {
	BaseURL         string            `json:"base_url"`
	Model           string            `json:"model"`
	Models          []Model           `json:"models,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	APIKey          string            `json:"api_key"`
	CatalogURL      string            `json:"model_catalog_url,omitempty"`
	CatalogMode     string            `json:"catalog_mode,omitempty"`
	AuthMode        string            `json:"auth_mode,omitempty"`
	Options         map[string]any    `json:"options,omitempty"`
	ProviderOptions map[string]any    `json:"provider_options,omitempty"`
}
type Profile struct {
	Remark          string          `json:"remark,omitempty"`
	Kind            string          `json:"kind"`
	Auth            json.RawMessage `json:"auth,omitempty"`
	AuthPendingSync bool            `json:"auth_pending_sync,omitempty"`
	Provider        *Provider       `json:"provider,omitempty"`
	Route           map[string]any  `json:"route,omitempty"`
	Usage           *UsageConfig    `json:"usage,omitempty"`
}
type Snapshot struct {
	Config       []byte `json:"config"`
	Auth         []byte `json:"auth"`
	ConfigExists bool   `json:"config_exists"`
	AuthExists   bool   `json:"auth_exists"`
}
type State struct {
	Home     string             `json:"home,omitempty"`
	Version  int                `json:"version"`
	Profiles map[string]Profile `json:"profiles"`
	Current  string             `json:"current"`
	Previous string             `json:"previous"`
	Baseline *Snapshot          `json:"baseline,omitempty"`
}
type Journal struct {
	Before    Snapshot
	After     Snapshot
	State     State
	Committed bool
}
type Store struct {
	Dir   string
	key   []byte
	fault func(string) error
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func ValidateName(name string) error {
	if !validName.MatchString(name) {
		return errors.New("名称须为 1–64 位字母、数字、下划线或连字符，并以字母或数字开头")
	}
	return nil
}

func privateDir(path string) error {
	if st, e := os.Lstat(path); e == nil {
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return errors.New("存储目录不能是符号链接或普通文件")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if e := os.MkdirAll(path, 0700); e != nil {
		return e
	}
	return os.Chmod(path, 0700)
}
func OpenStore(dir string, create bool) (*Store, error) {
	dir, e := filepath.Abs(dir)
	if e != nil {
		return nil, e
	}
	if create {
		if e = privateDir(dir); e != nil {
			return nil, e
		}
	}
	if !create {
		if st, err := os.Lstat(dir); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm() != 0700 {
			return nil, errors.New("账户存储目录不存在或不安全；请先添加账户")
		}
	}
	// Serialize key creation as well as vault operations.
	if create {
		guard := &Store{Dir: dir}
		unlock, err := guard.Lock()
		if err != nil {
			return nil, err
		}
		defer unlock()
	}
	sum := sha256.Sum256([]byte(dir))
	account := hex.EncodeToString(sum[:])
	raw, e := keyring.Get("codex-switch", account)
	if errors.Is(e, keyring.ErrNotFound) && create {
		if _, err := os.Stat(filepath.Join(dir, "vault.enc")); err == nil {
			return nil, errors.New("凭据库存在但 Keychain 密钥丢失；请恢复密钥，不能创建替代密钥")
		}
		k := make([]byte, 32)
		if _, e = rand.Read(k); e != nil {
			return nil, e
		}
		raw = base64.StdEncoding.EncodeToString(k)
		e = keyring.Set("codex-switch", account, raw)
	}
	if e != nil {
		return nil, errors.New("无法访问系统凭据库；请解锁 Keychain，未启用明文回退")
	}
	k, e := base64.StdEncoding.DecodeString(raw)
	if e != nil || len(k) != 32 {
		return nil, errors.New("凭据库密钥无效")
	}
	return &Store{Dir: dir, key: k}, nil
}
func (s *Store) Lock() (func(), error) {
	f, e := os.OpenFile(filepath.Join(s.Dir, "lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if e != nil {
		return nil, e
	}
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("锁文件不是普通文件")
	}
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("另一个账户管理操作正在运行")
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
func (s *Store) crypt() (cipher.AEAD, error) {
	b, e := aes.NewCipher(s.key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}
func (s *Store) write(name string, v any) error {
	if s.fault != nil {
		if err := s.fault("write:" + name); err != nil {
			return err
		}
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	a, e := s.crypt()
	if e != nil {
		return e
	}
	nonce := make([]byte, a.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return e
	}
	data := append([]byte("CXS1"), nonce...)
	data = a.Seal(data, nonce, raw, []byte(name))
	return atomicWriteWith(filepath.Join(s.Dir, name), data, func(stage string) error {
		if s.fault != nil {
			return s.fault("atomic:" + name + ":" + stage)
		}
		return nil
	})
}
func (s *Store) read(name string, v any) error {
	b, e := readSafe(filepath.Join(s.Dir, name))
	if e != nil {
		return e
	}
	a, e := s.crypt()
	if e != nil {
		return e
	}
	n := a.NonceSize()
	if len(b) < 4+n || string(b[:4]) != "CXS1" {
		return errors.New("凭据库存档格式无效")
	}
	raw, e := a.Open(nil, b[4:4+n], b[4+n:], []byte(name))
	if e != nil {
		return errors.New("凭据库解密或完整性验证失败")
	}
	if e = json.Unmarshal(raw, v); e != nil {
		return errors.New("凭据库存档结构无效")
	}
	return nil
}
func (s *Store) Load() (State, error) {
	var st State
	e := s.read("vault.enc", &st)
	if os.IsNotExist(e) {
		return State{Version: 2, Profiles: map[string]Profile{}}, nil
	}
	if e != nil {
		return st, e
	}
	if (st.Version != 1 && st.Version != 2) || st.Profiles == nil {
		return st, errors.New("不支持的凭据库版本")
	}
	st.Version = 2 // v1 aliases remain stable; optional remarks default to their ID.
	return normalizedStateConfig(st), nil
}

// Normalize only account options. Recovery snapshots must keep their exact
// original bytes, and loading a vault must never rewrite it without a save.
func normalizedStateConfig(st State) State {
	var profiles map[string]Profile
	for id, profile := range st.Profiles {
		route := normalizedAccountOptions(profile.Route)
		changed := len(route) != len(profile.Route)
		profile.Route = route
		if profile.Provider != nil {
			options := normalizedAccountOptions(profile.Provider.Options)
			if len(options) != len(profile.Provider.Options) {
				provider := *profile.Provider
				provider.Options = options
				profile.Provider = &provider
				changed = true
			}
		}
		if changed {
			if profiles == nil {
				profiles = make(map[string]Profile, len(st.Profiles))
				for id, profile := range st.Profiles {
					profiles[id] = profile
				}
			}
			profiles[id] = profile
		}
	}
	if profiles != nil {
		st.Profiles = profiles
	}
	return st
}
func (s *Store) Save(st State) error {
	st = normalizedStateConfig(st)
	err := s.saveTransaction(st)
	var write *replacedWriteError
	if !errors.As(err, &write) || write.Path != filepath.Join(s.Dir, "vault.enc") {
		return err
	}
	// Callers hold the store lock. Verify the intended plaintext state, rather
	// than encrypted bytes whose nonce changes on every write.
	var saved State
	st.Version = 2
	want, wantErr := json.Marshal(st)
	readErr := s.read("vault.enc", &saved)
	got, gotErr := json.Marshal(saved)
	if readErr != nil || wantErr != nil || gotErr != nil || !bytes.Equal(want, got) {
		return &OperationError{Unconfirmed: true, Cause: errors.New("存档替换后结果未确认；请检查状态，避免重复操作")}
	}
	return &OperationError{Committed: true, Cause: errors.New("存档已更新，但目录同步未确认；请保留备份，避免重复操作")}
}

// Transactional callers must retain their journal/rollback semantics. A vault
// rename alone does not mean that an account switch has committed.
func (s *Store) saveTransaction(st State) error {
	var previous State
	if err := s.read("vault.enc", &previous); err == nil && previous.Version == 1 {
		if _, err := os.Lstat(filepath.Join(s.Dir, "vault.v1.enc")); os.IsNotExist(err) {
			data, err := readSafe(filepath.Join(s.Dir, "vault.enc"))
			if err != nil {
				return err
			}
			if err = atomicWrite(filepath.Join(s.Dir, "vault.v1.enc"), data); err != nil {
				return err
			}
		}
	}
	st.Version = 2
	return s.write("vault.enc", normalizedStateConfig(st))
}

const maxFileSize = 8 << 20

func openDirectory(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
func readSafe(path string) ([]byte, error) {
	return readLimitedFile(path, maxFileSize)
}

// ReadImportFile rejects special files without blocking and caps allocation
// before parsing. Validation is on the open descriptor, not a prior path stat.
func ReadImportFile(path string) ([]byte, error) {
	return readLimitedFile(path, 1<<20)
}

func readLimitedFile(path string, limit int64) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("拒绝读取非普通文件")
	}
	if st.Size() > limit {
		return nil, fmt.Errorf("文件超出 %d MiB 限制", limit>>20)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("文件超出 %d MiB 限制", limit>>20)
	}
	return data, err
}
func atomicWrite(path string, b []byte) error {
	return atomicWriteWith(path, b, nil)
}

// A replacement is visible even if its directory sync subsequently fails.
// This is internal write-phase information, not an operation-level success.
type replacedWriteError struct {
	Path  string
	Cause error
}

func (e *replacedWriteError) Error() string { return e.Cause.Error() }
func (e *replacedWriteError) Unwrap() error { return e.Cause }

func atomicWriteWith(path string, b []byte, checkpoint func(string) error) error {
	check := func(stage string) error {
		if checkpoint != nil {
			return checkpoint(stage)
		}
		return nil
	}
	if len(b) > maxFileSize {
		return errors.New("文件超出 8 MiB 限制")
	}
	d, err := openDirectory(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	dirfd := int(d.Fd())
	name := filepath.Base(path)
	var st unix.Stat_t
	err = unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil && st.Mode&unix.S_IFMT != unix.S_IFREG {
		return errors.New("拒绝替换符号链接或非普通文件")
	}
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	tmp := ".codex-switch-" + hex.EncodeToString(random[:])
	fd, err := unix.Openat(dirfd, tmp, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(dirfd, tmp, 0)
	f := os.NewFile(uintptr(fd), tmp)
	if err = check("before_write"); err != nil {
		f.Close()
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = check("before_sync")
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// Both operations use the same directory descriptor, even if the path changes.
	if err = check("before_rename"); err != nil {
		return err
	}
	if err = unix.Renameat(dirfd, tmp, dirfd, name); err != nil {
		return err
	}
	if err = check("after_rename"); err != nil {
		return &replacedWriteError{Path: path, Cause: err}
	}
	if err = check("before_directory_sync"); err != nil {
		return &replacedWriteError{Path: path, Cause: err}
	}
	if err = d.Sync(); err != nil {
		return &replacedWriteError{Path: path, Cause: err}
	}
	return nil
}
func durableRemove(path string) error {
	d, err := openDirectory(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	var st unix.Stat_t
	err = unix.Fstatat(int(d.Fd()), filepath.Base(path), &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return errors.New("拒绝删除非普通文件")
	}
	if err = unix.Unlinkat(int(d.Fd()), filepath.Base(path), 0); err != nil {
		return err
	}
	return d.Sync()
}
func (s *Store) HasJournal() bool {
	_, e := os.Lstat(filepath.Join(s.Dir, "pending.enc"))
	return !os.IsNotExist(e)
}
func (s *Store) ClearJournal() error {
	if s.fault != nil {
		if err := s.fault("clear:pending.enc"); err != nil {
			return err
		}
	}
	return durableRemove(filepath.Join(s.Dir, "pending.enc"))
}
func safeError(operation string, err error) error {
	return fmt.Errorf("%s失败（%T）；未输出凭据，请使用 status 检查状态", operation, err)
}
