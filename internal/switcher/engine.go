package switcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Engine struct {
	Store      *Store
	Home       string
	Helper     string
	Runtime    Runtime
	Context    context.Context
	OnMessage  func(string)
	checkpoint func(string) error
}

func (e Engine) state() (State, error) {
	st, err := e.Store.Load()
	if err != nil {
		return st, err
	}
	if st.Home != "" && filepath.Clean(st.Home) != filepath.Clean(e.Home) {
		return st, errors.New("此账户存储已绑定另一 Codex 目录，请为其他目录使用独立 --store")
	}
	st.Home = e.Home
	return st, nil
}

func snapshot(home string) (Snapshot, error) {
	var s Snapshot
	var e error
	s.Config, e = readSafe(filepath.Join(home, "config.toml"))
	if e != nil && !os.IsNotExist(e) {
		return s, e
	}
	s.ConfigExists = e == nil
	s.Auth, e = readSafe(filepath.Join(home, "auth.json"))
	if e != nil && !os.IsNotExist(e) {
		return s, e
	}
	s.AuthExists = e == nil
	return s, nil
}
func applySnapshot(home string, s Snapshot) error { return applySnapshotWith(home, s, nil) }
func applySnapshotWith(home string, s Snapshot, checkpoint func(string) error) error {
	for _, f := range []struct {
		name   string
		b      []byte
		exists bool
	}{{"config.toml", s.Config, s.ConfigExists}, {"auth.json", s.Auth, s.AuthExists}} {
		if checkpoint != nil {
			if err := checkpoint("before:" + f.name); err != nil {
				return err
			}
		}
		p := filepath.Join(home, f.name)
		if f.exists {
			if e := atomicWriteWith(p, f.b, func(stage string) error {
				if checkpoint != nil {
					return checkpoint(f.name + ":" + stage)
				}
				return nil
			}); e != nil {
				return e
			}
		} else {
			if e := durableRemove(p); e != nil {
				return e
			}
		}
		if checkpoint != nil {
			if err := checkpoint("after:" + f.name); err != nil {
				return err
			}
		}
	}
	return nil
}
func same(a, b Snapshot) bool {
	return a.ConfigExists == b.ConfigExists && a.AuthExists == b.AuthExists && bytes.Equal(a.Config, b.Config) && bytes.Equal(a.Auth, b.Auth)
}
func (e Engine) checkPending() error {
	if err := e.contextErr(); err != nil {
		return err
	}
	if e.Store.HasJournal() {
		return &OperationError{Pending: true, Cause: errors.New("发现未完成切换，请先运行 codex-switch recover")}
	}
	return nil
}
func preserve(st *State, s Snapshot) error {
	if !s.AuthExists {
		return nil
	}
	id, err := AuthIdentity(s.Auth)
	if err != nil {
		if st.Current != "" {
			p := st.Profiles[st.Current]
			if p.Kind == "provider" && p.Provider != nil {
				var d map[string]any
				if json.Unmarshal(s.Auth, &d) == nil && d["OPENAI_API_KEY"] == p.Provider.APIKey {
					return nil
				}
			}
		}
		return errors.New("当前认证不属于已管理账户，请先导入当前官方账户或还原外部修改")
	}
	for name, p := range st.Profiles {
		if p.Kind != "chatgpt" {
			continue
		}
		saved, err := AuthIdentity(p.Auth)
		if err == nil && id == saved {
			// A saved rotation may not have reached the live file before cancel.
			// Its refresh token remains the newest known one even after expiry.
			if !p.AuthPendingSync || bytes.Equal(p.Auth, s.Auth) {
				p.Auth = append([]byte(nil), s.Auth...)
				p.AuthPendingSync = false
			}
			if st.Current == name {
				route, err := Route(s.Config)
				if err != nil {
					return err
				}
				if v, ok := route["model_provider"]; ok && v != "openai" {
					return errors.New("官方账户当前使用外部路由，请先还原官方配置")
				}
				p.Route = route
			}
			st.Profiles[name] = p
			st.Current = name
			return nil
		}
	}
	return errors.New("检测到未管理的官方登录，请先运行 add chatgpt <名称> --current")
}
func (e Engine) AddChatGPT(name string, current, device bool) error {
	if err := ValidateRemark(name); err != nil {
		return err
	}
	remark := name
	name = ProfileID(name)
	unlock, err := e.Store.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err = e.checkPending(); err != nil {
		return err
	}
	st, err := e.state()
	if err != nil {
		return err
	}
	if _, ok := st.Profiles[name]; ok {
		return errors.New("名称已存在")
	}
	home := e.Home
	if !current {
		home, err = os.MkdirTemp(e.Store.Dir, "login-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(home)
		if err = e.Runtime.Login(home, device); err != nil {
			return err
		}
	}
	s, err := snapshot(home)
	if err != nil {
		return err
	}
	id, err := AuthIdentity(s.Auth)
	if err != nil {
		return err
	}
	for _, p := range st.Profiles {
		if p.Kind == "chatgpt" {
			other, _ := AuthIdentity(p.Auth)
			if other == id {
				return errors.New("该官方账户及工作空间已存在；请避免重复登录同一账户")
			}
		}
	}
	live, err := snapshot(e.Home)
	if err != nil {
		return err
	}
	route, err := Route(live.Config)
	if err != nil {
		return err
	}
	delete(route, "openai_base_url")
	delete(route, "chatgpt_base_url")
	delete(route, "profile")
	if !current {
		delete(route, "forced_chatgpt_workspace_id")
	}
	if v, ok := route["model_provider"]; ok && v != "openai" {
		delete(route, "model")
		delete(route, "model_catalog_json")
		for k := range accountRoot {
			delete(route, k)
		}
		delete(route, "features")
	}
	st.Profiles[name] = Profile{Kind: "chatgpt", Remark: remark, Auth: s.Auth, Route: route}
	if current {
		st.Current = name
	}
	return e.Store.Save(st)
}
func (e Engine) AddProvider(name string, p Provider) error {
	if err := ValidateRemark(name); err != nil {
		return err
	}
	remark := name
	name = ProfileID(name)
	if err := ValidateProvider(p); err != nil {
		return err
	}
	unlock, err := e.Store.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err = e.checkPending(); err != nil {
		return err
	}
	st, err := e.state()
	if err != nil {
		return err
	}
	if _, ok := st.Profiles[name]; ok {
		return errors.New("名称已存在")
	}
	st.Profiles[name] = Profile{Kind: "provider", Remark: remark, Provider: &p}
	return e.Store.Save(st)
}
func (e Engine) Use(name string) error {
	return e.switchProfile(name, nil)
}

func (e Engine) UseForced(name string) error {
	r, ok := e.Runtime.(MacRuntime)
	if !ok {
		return errors.New("当前运行环境不支持强制切换")
	}
	r.Force = true
	e.Runtime = r
	return e.Use(name)
}
func (e Engine) switchProfile(name string, update *ModelUpdate) (result error) {
	unlock, err := e.Store.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if e.Context != nil && e.Context.Err() != nil {
		return e.Context.Err()
	}
	stage := "preflight"
	event := AuditEvent{Operation: "switch", ID: auditID(), Status: "started"}
	if update != nil {
		event.Operation = "update_models"
		if update.Provider != nil {
			event.Operation = "update_provider"
		}
	}
	if ValidateName(name) == nil {
		event.To = name
	}
	if r, ok := e.Runtime.(MacRuntime); ok {
		event.Force = r.Force
	}
	if current, loadErr := e.Store.Load(); loadErr == nil {
		event.From = current.Current
	}
	if err = e.Store.appendAudit(event); err != nil {
		return err
	}
	defer func() {
		if result != nil && e.Store.HasJournal() && !NeedsRecovery(result) {
			result = &OperationError{Pending: true, Cause: result}
		}
		if logErr := e.finishAudit(event, stage, result); logErr != nil {
			if result == nil {
				result = &OperationError{Committed: true, Cause: logErr}
			} else {
				result = errors.Join(result, logErr)
			}
		}
	}()
	if err = e.checkPending(); err != nil {
		return err
	}
	st, err := e.state()
	if err != nil {
		return err
	}
	if name == "" {
		name = st.Previous
		if name == "" {
			return errors.New("没有上一个已管理账户；请先导入当前账户，再进行切换")
		}
	}
	if ValidateName(name) == nil {
		event.To = name
	}
	target, ok := st.Profiles[name]
	if !ok {
		return errors.New("账户不存在")
	}
	if update != nil {
		if target.Kind != "provider" || target.Provider == nil {
			return errors.New("模型列表配置仅适用于第三方 provider")
		}
		p := *target.Provider
		if update.Provider != nil {
			p = *update.Provider
		} else if update.CatalogMode == "remote" {
			p.CatalogMode = "remote"
			if p.CatalogURL == "" {
				return errors.New("该账户未配置远程模型目录")
			}
		} else {
			p.Models = append([]Model(nil), update.Models...)
			p.Model = update.Default
			p.CatalogMode = "local"
		}
		if err = ValidateProvider(p); err != nil {
			return err
		}
		target.Provider = &p
		if st.Current != name {
			stage = "save_profile"
			st.Profiles[name] = target
			return e.Store.Save(st)
		}
	}
	before, err := snapshot(e.Home)
	if err != nil {
		return err
	}
	if _, err = ParseConfig(before.Config); err != nil {
		return err
	}
	// Preflight completes before interrupting the desktop.
	planned, err := e.plan(before, target, name)
	if err != nil {
		return err
	}
	if runtime, ok := e.Runtime.(interface{ Preflight() error }); ok {
		if err := runtime.Preflight(); err != nil {
			return err
		}
	}
	stage = "stop"
	_, err = e.Runtime.Stop()
	if err != nil {
		return err
	}
	before, err = snapshot(e.Home)
	if err != nil {
		return err
	}
	if err = preserve(&st, before); err != nil {
		return err
	}
	if update == nil {
		target = st.Profiles[name]
	}
	if target.Kind == "chatgpt" {
		stage = "authenticate"
		target, err = e.reauthenticate(&st, name, target)
		if err != nil {
			return err
		}
		// Renewing the current account must not roll back to its rotated old token.
		if st.Current == name && !bytes.Equal(before.Auth, target.Auth) {
			live, readErr := snapshot(e.Home)
			if readErr != nil || !same(live, before) {
				return errors.New("认证期间检测到外部配置修改，最新凭据已保存，但未继续切换")
			}
			if err = e.ensureStopped(); err != nil {
				return err
			}
			if err = e.contextErr(); err != nil {
				return err
			}
			if err = atomicWrite(filepath.Join(e.Home, "auth.json"), target.Auth); err != nil {
				return errors.New("最新凭据已保存，但当前认证更新失败；请重试切换")
			}
			before.Auth = append([]byte(nil), target.Auth...)
		}
	}
	planned, err = e.plan(before, target, name)
	if err != nil {
		return err
	}
	if err = e.Store.saveTransaction(st); err != nil {
		return err
	}
	old := st
	old.Profiles = make(map[string]Profile, len(st.Profiles))
	for k, v := range st.Profiles {
		old.Profiles[k] = v
	}
	if st.Current == "" {
		st.Baseline = &before
	}
	if st.Current != name {
		st.Previous = st.Current
	}
	st.Current = name
	target.AuthPendingSync = false
	st.Profiles[name] = target
	j := Journal{Before: before, After: planned, State: old}
	if err = e.Store.write("pending.enc", j); err != nil {
		return err
	}
	if err := e.boundary("journal_prepared"); err != nil {
		return &OperationError{Pending: true, Cause: err}
	}
	rollback := func(cause error) error {
		if er := e.revert(j); er != nil {
			return &OperationError{Pending: true, Cause: errors.Join(errors.New("切换失败且回滚未完成；请运行 recover，避免继续修改认证"), cause)}
		}
		event.RolledBack = true
		return &OperationError{RolledBack: true, Cause: cause}
	}
	live, err := snapshot(e.Home)
	if err != nil || !same(live, before) {
		return rollback(errors.New("检测到外部配置写入，切换停止"))
	}
	if err = e.ensureStopped(); err != nil {
		return rollback(err)
	}
	if e.Context != nil && e.Context.Err() != nil {
		return rollback(e.Context.Err())
	}
	stage = "write"
	if err = applySnapshotWith(e.Home, planned, e.checkpoint); err != nil {
		return rollback(safeError("写入", err))
	}
	if err := e.boundary("snapshot_applied"); err != nil {
		return rollback(err)
	}
	stage = "verify"
	if err = e.Runtime.Verify(e.Home); err != nil {
		return rollback(err)
	}
	if err := e.boundary("verified"); err != nil {
		return rollback(err)
	}
	if err = e.contextErr(); err != nil {
		return rollback(err)
	}
	if err = e.Store.saveTransaction(st); err != nil {
		return rollback(safeError("保存状态", err))
	}
	if err := e.boundary("state_saved"); err != nil {
		return rollback(err)
	}
	j.Committed = true
	if err = e.Store.write("pending.enc", j); err != nil {
		return &OperationError{Pending: true, Cause: errors.New("提交记录写入未确认，请运行 recover；将依据持久化记录完成恢复")}
	}
	if err := e.boundary("journal_committed"); err != nil {
		return &OperationError{Committed: true, Pending: true, Cause: err}
	}
	if err = e.Store.ClearJournal(); err != nil {
		return &OperationError{Committed: true, Pending: true, Cause: errors.New("账户已切换；请运行 recover 清理提交记录")}
	}
	if err := e.boundary("journal_cleared"); err != nil {
		return &OperationError{Committed: true, Cause: err}
	}
	stage = "complete"
	if err := e.pruneCatalogs(st); err != nil {
		return &OperationError{Committed: true, Cause: errors.New("账户已切换；旧模型目录清理未完成")}
	}
	return nil
}
func (e Engine) plan(before Snapshot, p Profile, name string) (Snapshot, error) {
	s := before
	s.ConfigExists = true
	s.AuthExists = true
	var err error
	if p.Kind == "chatgpt" {
		if _, err = AuthIdentity(p.Auth); err != nil {
			return s, err
		}
		s.Config, err = PatchConfig(before.Config, p.Route, nil, e.Helper, e.Store.Dir, name, e.Home)
		s.Auth = append([]byte(nil), p.Auth...)
	} else if p.Kind == "provider" && p.Provider != nil {
		if err = ValidateProvider(*p.Provider); err != nil {
			return s, err
		}
		route := map[string]any{}
		for k, v := range p.Provider.Options {
			route[k] = v
		}
		if p.Provider.CatalogMode != "remote" {
			catalog, ce := e.catalog(*p.Provider)
			if ce != nil {
				return s, ce
			}
			route["model_catalog_json"] = catalog
		}
		s.Config, err = PatchConfig(before.Config, route, p.Provider, e.Helper, e.Store.Dir, name, e.Home)
		s.Auth, _ = json.Marshal(map[string]any{"OPENAI_API_KEY": p.Provider.APIKey})
	} else {
		return s, errors.New("账户类型无效")
	}
	return s, err
}
func (e Engine) revert(j Journal) error {
	live, err := snapshot(e.Home)
	if err != nil {
		return err
	}
	if !((bytes.Equal(live.Config, j.Before.Config) && live.ConfigExists == j.Before.ConfigExists) || (bytes.Equal(live.Config, j.After.Config) && live.ConfigExists == j.After.ConfigExists)) {
		return errors.New("配置被外部修改，拒绝覆盖")
	}
	if !((bytes.Equal(live.Auth, j.Before.Auth) && live.AuthExists == j.Before.AuthExists) || (bytes.Equal(live.Auth, j.After.Auth) && live.AuthExists == j.After.AuthExists)) {
		return errors.New("认证被外部修改，拒绝覆盖")
	}
	if !same(live, j.Before) {
		if err = e.ensureStopped(); err != nil {
			return err
		}
		if err = applySnapshotWith(e.Home, j.Before, e.checkpoint); err != nil {
			return err
		}
	}
	if err = e.Store.saveTransaction(j.State); err != nil {
		return err
	}
	return e.Store.ClearJournal()
}
func (e Engine) Recover() (result error) {
	unlock, err := e.Store.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	event := AuditEvent{Operation: "recover", ID: auditID(), Status: "started"}
	if err = e.Store.appendAudit(event); err != nil {
		return err
	}
	defer func() {
		if logErr := e.finishAudit(event, "recover", result); logErr != nil {
			if result == nil {
				result = &OperationError{Committed: true, Cause: logErr}
			} else {
				result = errors.Join(result, logErr)
			}
		}
	}()
	var j Journal
	if err = e.Store.read("pending.enc", &j); os.IsNotExist(err) {
		return errors.New("没有未完成切换")
	} else if err != nil {
		return err
	}
	// Recovery bypasses state(), so verify both persisted bindings before
	// stopping anything, touching the target directory, or clearing a journal.
	st, err := e.Store.Load()
	if err != nil {
		return err
	}
	if st.Home == "" && j.State.Home == "" {
		return &OperationError{Pending: true, Cause: errors.New("旧恢复记录缺少 Codex 目录绑定，拒绝自动恢复；请核查原目录和备份")}
	}
	for _, home := range []string{st.Home, j.State.Home} {
		if home != "" && filepath.Clean(home) != filepath.Clean(e.Home) {
			return &OperationError{Pending: true, Cause: errors.New("恢复记录绑定另一 Codex 目录，请使用原 --home 与 --store")}
		}
	}
	j.State.Home = e.Home // An old journal can inherit an explicit vault binding.
	if err = e.contextErr(); err != nil {
		return &OperationError{Pending: true, Cause: err}
	}
	if j.Committed {
		live, err := snapshot(e.Home)
		if err != nil {
			return err
		}
		if !same(live, j.After) {
			return &OperationError{Committed: true, Pending: true, Cause: errors.New("切换已提交，但配置随后被外部修改；请核查状态，恢复未覆盖外部修改")}
		}
		if st.Home == "" {
			st.Home = e.Home
			if err = e.Store.saveTransaction(st); err != nil {
				return &OperationError{Committed: true, Pending: true, Cause: errors.New("账户已切换，旧存档目录绑定保存未确认；请运行 recover 完成清理")}
			}
		}
		if err = e.Store.ClearJournal(); err != nil {
			return &OperationError{Committed: true, Pending: true, Cause: err}
		}
		return nil
	}
	_, err = e.Runtime.Stop()
	if err != nil {
		return err
	}
	return e.revert(j)
}
func (e Engine) Restore() error {
	return e.Use("")
}
func (e Engine) Remove(name string) error {
	unlock, err := e.Store.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err = e.checkPending(); err != nil {
		return err
	}
	st, err := e.state()
	if err != nil {
		return err
	}
	if name == st.Current {
		return errors.New("不能删除当前账户")
	}
	if _, ok := st.Profiles[name]; !ok {
		return errors.New("账户不存在")
	}
	if name == st.Previous {
		st.Previous = ""
	}
	delete(st.Profiles, name)
	return e.Store.Save(st)
}
func (e Engine) Credential(name string) (string, error) {
	if e.Store.HasJournal() {
		var j Journal
		if err := e.Store.read("pending.enc", &j); err != nil {
			return "", err
		}
		live, err := snapshot(e.Home)
		if err != nil || !same(live, j.After) {
			return "", errors.New("切换尚未完成，认证暂不可用")
		}
	}
	st, err := e.state()
	if err != nil {
		return "", err
	}
	p, ok := st.Profiles[name]
	if !ok || p.Kind != "provider" || p.Provider == nil || st.Current != name {
		return "", errors.New("目标第三方账户未生效")
	}
	return p.Provider.APIKey, nil
}

func (e Engine) UseContext(ctx context.Context, name string) error {
	return e.WithContext(ctx).Use(name)
}
func (e Engine) UseForcedContext(ctx context.Context, name string) error {
	return e.WithContext(ctx).UseForced(name)
}
func (e Engine) WithContext(ctx context.Context) Engine {
	e.Context = ctx
	if r, ok := e.Runtime.(MacRuntime); ok {
		r.Context = ctx
		e.Runtime = r
	}
	return e
}

func (e Engine) contextErr() error {
	if e.Context != nil {
		return e.Context.Err()
	}
	return nil
}

func (e Engine) boundary(stage string) error {
	if e.checkpoint != nil {
		return e.checkpoint(stage)
	}
	return nil
}
