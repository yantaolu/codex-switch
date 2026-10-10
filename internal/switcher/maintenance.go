package switcher

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

const auditLimit = 1 << 20
const auditCopies = 3

func (s *Store) rotateAudit() error {
	path := filepath.Join(s.Dir, "switch.log")
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errors.New("日志不是普通文件")
	}
	if st.Size() < auditLimit {
		return nil
	}
	for n := auditCopies; n >= 1; n-- {
		to := path + "." + string(rune('0'+n))
		from := path
		if n > 1 {
			from = path + "." + string(rune('0'+n-1))
		}
		if info, err := os.Lstat(to); err == nil && !info.Mode().IsRegular() {
			return errors.New("轮转日志不是普通文件")
		}
		if info, err := os.Lstat(from); err == nil {
			if !info.Mode().IsRegular() {
				return errors.New("轮转日志不是普通文件")
			}
			if err = os.Rename(from, to); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	d, err := openDirectory(s.Dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Reads at most 1 MiB per retained file, including oversized legacy logs.
func RecentAudit(dir string) ([]AuditEvent, error) {
	events := []AuditEvent{}
	for n := auditCopies; n >= 0; n-- {
		name := filepath.Join(dir, "switch.log")
		if n > 0 {
			name += "." + string(rune('0'+n))
		}
		f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			f.Close()
			return nil, errors.New("日志不是普通文件")
		}
		start := max(int64(0), st.Size()-auditLimit)
		scan := bufio.NewScanner(io.NewSectionReader(f, start, st.Size()-start))
		scan.Buffer(make([]byte, 4096), 64<<10)
		if start > 0 {
			scan.Scan()
		} // Discard a truncated first record.
		for scan.Scan() {
			var e AuditEvent
			if json.Unmarshal(scan.Bytes(), &e) == nil && e.ID != "" {
				events = append(events, e)
				if len(events) > 200 {
					events = events[1:]
				}
			}
		}
		err = scan.Err()
		f.Close()
		if err != nil {
			return nil, errors.New("日志记录过长或读取失败")
		}
	}
	return events, nil
}

var catalogName = regexp.MustCompile(`^[0-9a-f]{64}\.json$`)

func (e Engine) pruneCatalogs(st State) error {
	if e.Store.HasJournal() {
		return nil
	}
	dir := filepath.Join(e.Store.Dir, "catalogs")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, p := range st.Profiles {
		if p.Provider != nil && p.Provider.CatalogMode != "remote" {
			raw, err := Catalog(*p.Provider)
			if err != nil {
				return err
			}
			keep[catalogFilename(raw)] = true
		}
	}
	snapshots := []Snapshot{}
	if st.Baseline != nil {
		snapshots = append(snapshots, *st.Baseline)
	}
	live, err := snapshot(e.Home)
	if err != nil {
		return err
	}
	snapshots = append(snapshots, live)
	for _, s := range snapshots {
		route, err := Route(s.Config)
		if err != nil {
			return err
		}
		path, _ := route["model_catalog_json"].(string)
		if filepath.Dir(path) == dir {
			keep[filepath.Base(path)] = true
		}
	}
	for _, entry := range entries {
		if !catalogName.MatchString(entry.Name()) || keep[entry.Name()] {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			continue
		}
		if err := durableRemove(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
