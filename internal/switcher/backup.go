package switcher

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const backupHeader = "CXSB1"
const backupIterations = 600000

func backupCipher(password string, salt []byte) (cipher.AEAD, error) {
	if len(password) < 12 || len(password) > 4096 {
		return nil, errors.New("备份密码须为 12–4096 字节")
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, backupIterations, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func validateBackupState(st State) error {
	if (st.Version != 1 && st.Version != 2) || st.Profiles == nil || len(st.Profiles) > 1000 {
		return errors.New("备份账户结构或版本无效")
	}
	for id, p := range st.Profiles {
		if ValidateName(id) != nil || p.Remark != "" && ValidateRemark(p.Remark) != nil {
			return errors.New("备份账户备注或 ID 无效")
		}
		switch p.Kind {
		case "chatgpt":
			if _, err := AuthIdentity(p.Auth); err != nil {
				return errors.New("备份包含无效官方认证")
			}
		case "provider":
			if p.Provider == nil || ValidateProvider(*p.Provider) != nil {
				return errors.New("备份包含无效服务商")
			}
		default:
			return errors.New("备份账户类型无效")
		}
		if p.Usage != nil && ValidateUsage(*p.Usage) != nil {
			return errors.New("备份额度查询配置无效")
		}
	}
	return nil
}
func (e Engine) ExportBackup(path, password string) error {
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
	live, err := snapshot(e.Home)
	if err != nil {
		return err
	}
	if st.Current != "" {
		if err = preserve(&st, live); err != nil {
			return err
		}
	}
	if err = validateBackupState(st); err != nil {
		return err
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	salt := make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return err
	}
	a, err := backupCipher(password, salt)
	if err != nil {
		return err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	header := append([]byte(backupHeader), salt...)
	header = append(header, nonce...)
	data := a.Seal(header, nonce, raw, header)
	// Backups never overwrite a previous recovery copy.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("备份文件已存在或无法创建")
	}
	failed := true
	defer func() {
		f.Close()
		if failed {
			os.Remove(path)
		}
	}()
	if len(data) > maxFileSize {
		return errors.New("备份超出 8 MiB 限制")
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	dir, err := openDirectory(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return err
	}
	failed = false
	return nil
}
func DecodeBackup(data []byte, password string) (State, error) {
	var st State
	const headerSize = len(backupHeader) + 16 + 12
	if len(data) > maxFileSize || len(data) < headerSize+16 || string(data[:len(backupHeader)]) != backupHeader {
		return st, errors.New("备份格式无效")
	}
	a, err := backupCipher(password, data[len(backupHeader):len(backupHeader)+16])
	if err != nil {
		return st, err
	}
	raw, err := a.Open(nil, data[headerSize-12:headerSize], data[headerSize:], data[:headerSize])
	if err != nil {
		return st, errors.New("备份密码不正确或文件损坏")
	}
	if json.Unmarshal(raw, &st) != nil {
		return st, errors.New("备份内容无效")
	}
	return st, validateBackupState(st)
}
func (e Engine) ImportBackup(path, password string) error {
	data, err := readSafe(path)
	if err != nil {
		return err
	}
	st, err := DecodeBackup(data, password)
	if err != nil {
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
	existing, err := e.Store.Load()
	if err != nil {
		return err
	}
	if len(existing.Profiles) > 0 {
		return errors.New("恢复须使用空账户库；可用 --store 指定新目录，避免覆盖已有账户")
	}
	st.Version = 2
	st.Home = e.Home
	st.Current = ""
	st.Previous = ""
	st.Baseline = nil
	return e.Store.Save(st)
}
