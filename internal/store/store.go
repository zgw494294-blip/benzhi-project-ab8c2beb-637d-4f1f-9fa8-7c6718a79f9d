package store

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"benzhi-project-ab8c2beb-637d-4f1f-9fa8-7c6718a79f9d/internal/archive"
)

type CommitRequest struct {
	Archive         *archive.InterviewArchive
	ExpectedVersion int64
	Actor           string
	Action          string
	Detail          string
	ActionKey       string
	Result          []byte
	At              time.Time
}

type Repository interface {
	Load(id string) (*archive.InterviewArchive, error)
	List() ([]*archive.InterviewArchive, error)
	Commit(request CommitRequest) (archive.AuditEvent, error)
	Audit(id string) ([]archive.AuditEvent, error)
	AuditDiagnostics(id string) ([]archive.AuditEvent, AuditIntegrity, error)
	FindAction(id, key string) (archive.AuditEvent, bool, error)
	SaveArtifact(archiveID, name string, data []byte) error
	LoadArtifact(archiveID, name string) ([]byte, error)
}

type AuditIntegrity struct {
	Intact         bool      `json:"intact"`
	EventCount     int       `json:"eventCount"`
	FirstEventAt   time.Time `json:"firstEventAt,omitempty"`
	LastEventAt    time.Time `json:"lastEventAt,omitempty"`
	LastEventHash  string    `json:"lastEventHash,omitempty"`
	BrokenSequence int64     `json:"brokenSequence,omitempty"`
	Reason         string    `json:"reason,omitempty"`
}

type JSONStore struct {
	root      string
	archives  string
	audits    string
	artifacts string
	lockGuard sync.Mutex
	locks     map[string]*sync.Mutex
}

func Open(root string) (*JSONStore, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("存储目录不能为空")
	}
	s := &JSONStore{root: root, archives: filepath.Join(root, "archives"), audits: filepath.Join(root, "audit"), artifacts: filepath.Join(root, "manifests"), locks: map[string]*sync.Mutex{}}
	for _, dir := range []string{s.root, s.archives, s.audits, s.artifacts} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("创建存储目录 %s: %w", dir, err)
		}
	}
	if err := syncDirectory(s.root); err != nil {
		return nil, err
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *JSONStore) Validate() error {
	validated := map[string][]archive.AuditEvent{}
	entries, err := os.ReadDir(s.audits)
	if err != nil {
		return fmt.Errorf("读取审计目录: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		events, err := s.Audit(id)
		if err != nil {
			return fmt.Errorf("档案 %s 审计日志损坏: %w", id, err)
		}
		validated[id] = events
	}
	snapshots, err := os.ReadDir(s.archives)
	if err != nil {
		return fmt.Errorf("读取档案目录: %w", err)
	}
	for _, entry := range snapshots {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		value, err := s.Load(id)
		if err != nil {
			return err
		}
		events := validated[id]
		if len(events) == 0 {
			return fmt.Errorf("档案 %s 快照缺少审计记录", id)
		}
		if latest := events[len(events)-1].ArchiveVersion; latest != value.Version {
			return fmt.Errorf("档案 %s 快照版本 %d 与审计末端版本 %d 不匹配", id, value.Version, latest)
		}
		delete(validated, id)
	}
	for id := range validated {
		if _, err := os.Stat(s.snapshotPath(id)); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("档案 %s 有审计记录但缺少快照", id)
		}
	}
	return nil
}

func (s *JSONStore) Load(id string) (*archive.InterviewArchive, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.snapshotPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, archive.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取档案快照: %w", err)
	}
	var value archive.InterviewArchive
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("解析档案 %s 快照: %w", id, err)
	}
	if value.ID != id {
		return nil, fmt.Errorf("档案快照 ID %s 与文件名不匹配", value.ID)
	}
	if !value.Status.Valid() {
		return nil, fmt.Errorf("档案 %s 状态无效", id)
	}
	value.Normalize()
	return &value, nil
}

func (s *JSONStore) List() ([]*archive.InterviewArchive, error) {
	entries, err := os.ReadDir(s.archives)
	if err != nil {
		return nil, fmt.Errorf("读取档案目录: %w", err)
	}
	result := []*archive.InterviewArchive{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		value, err := s.Load(id)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
}

func (s *JSONStore) Commit(request CommitRequest) (archive.AuditEvent, error) {
	if request.Archive == nil {
		return archive.AuditEvent{}, errors.New("提交档案不能为空")
	}
	if err := validateID(request.Archive.ID); err != nil {
		return archive.AuditEvent{}, err
	}
	if strings.TrimSpace(request.Actor) == "" || strings.TrimSpace(request.Action) == "" {
		return archive.AuditEvent{}, errors.New("审计参与者和动作不能为空")
	}
	lock := s.archiveLock(request.Archive.ID)
	lock.Lock()
	defer lock.Unlock()
	currentVersion := int64(0)
	current, err := s.Load(request.Archive.ID)
	if err == nil {
		currentVersion = current.Version
	} else if !errors.Is(err, archive.ErrNotFound) {
		return archive.AuditEvent{}, err
	}
	events, err := s.readAudit(request.Archive.ID)
	if err != nil {
		return archive.AuditEvent{}, err
	}
	if request.ActionKey != "" {
		for _, existing := range events {
			if existing.ActionKey == request.ActionKey {
				request.Archive.Version = existing.ArchiveVersion
				return existing, nil
			}
		}
	}
	if currentVersion != request.ExpectedVersion {
		return archive.AuditEvent{}, fmt.Errorf("%w: 预期 %d，当前 %d", archive.ErrVersionConflict, request.ExpectedVersion, currentVersion)
	}
	if request.Archive.Version != request.ExpectedVersion {
		return archive.AuditEvent{}, errors.New("提交对象的版本基线不正确")
	}
	committed := cloneArchive(request.Archive)
	committed.Version = request.ExpectedVersion + 1
	if committed.FrozenVersion == request.ExpectedVersion+1 && committed.Status == archive.StatusPendingApproval {
		committed.FrozenVersion = committed.Version
	}
	committed.UpdatedAt = request.At.UTC()
	if request.At.IsZero() {
		committed.UpdatedAt = time.Now().UTC()
	}
	event := archive.AuditEvent{Sequence: int64(len(events) + 1), ArchiveID: committed.ID, At: committed.UpdatedAt, Actor: strings.TrimSpace(request.Actor), Action: strings.TrimSpace(request.Action), Detail: strings.TrimSpace(request.Detail), ArchiveVersion: committed.Version, ActionKey: request.ActionKey, Result: request.Result}
	if len(event.Result) == 0 {
		event.Result, err = json.Marshal(committed)
		if err != nil {
			return archive.AuditEvent{}, fmt.Errorf("编码幂等提交结果: %w", err)
		}
	}
	if len(events) > 0 {
		event.PreviousHash = events[len(events)-1].Hash
	}
	event.Hash = hashEvent(event)
	if err := s.writeSnapshot(committed); err != nil {
		return archive.AuditEvent{}, err
	}
	if err := s.appendAudit(event); err != nil {
		return archive.AuditEvent{}, err
	}
	request.Archive.Version = committed.Version
	request.Archive.UpdatedAt = committed.UpdatedAt
	request.Archive.FrozenVersion = committed.FrozenVersion
	return event, nil
}

func (s *JSONStore) Audit(id string) ([]archive.AuditEvent, error) {
	if err := validateID(id); err != nil {
		return nil, err
	}
	lock := s.archiveLock(id)
	lock.Lock()
	defer lock.Unlock()
	return s.readAudit(id)
}

func (s *JSONStore) AuditDiagnostics(id string) ([]archive.AuditEvent, AuditIntegrity, error) {
	if err := validateID(id); err != nil {
		return nil, AuditIntegrity{}, err
	}
	lock := s.archiveLock(id)
	lock.Lock()
	defer lock.Unlock()
	file, err := os.Open(s.auditPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return []archive.AuditEvent{}, AuditIntegrity{Intact: true}, nil
	}
	if err != nil {
		return nil, AuditIntegrity{}, fmt.Errorf("打开审计日志: %w", err)
	}
	defer file.Close()
	events := []archive.AuditEvent{}
	integrity := AuditIntegrity{Intact: true}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	line, previous := 0, ""
	for scanner.Scan() {
		line++
		var event archive.AuditEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			integrity.Intact, integrity.BrokenSequence, integrity.Reason = false, int64(line), fmt.Sprintf("第 %d 条事件 JSON 无效", line)
			break
		}
		reason := auditEventProblem(event, id, line, previous)
		if reason != "" {
			integrity.Intact, integrity.BrokenSequence, integrity.Reason = false, int64(line), reason
			break
		}
		events = append(events, event)
		previous = event.Hash
	}
	if !integrity.Intact {
		for scanner.Scan() {
			line++
		}
	}
	if err := scanner.Err(); err != nil {
		return events, integrity, fmt.Errorf("读取审计日志第 %d 行附近: %w", line+1, err)
	}
	integrity.EventCount = line
	if len(events) > 0 {
		integrity.FirstEventAt = events[0].At.UTC()
		integrity.LastEventAt = events[len(events)-1].At.UTC()
		integrity.LastEventHash = events[len(events)-1].Hash
	}
	return events, integrity, nil
}

func auditEventProblem(event archive.AuditEvent, id string, line int, previous string) string {
	if event.Sequence != int64(line) {
		return fmt.Sprintf("第 %d 条事件序号为 %d，应为 %d", line, event.Sequence, line)
	}
	if event.ArchiveVersion != int64(line) {
		return fmt.Sprintf("第 %d 条事件的档案版本为 %d，版本不连续", line, event.ArchiveVersion)
	}
	if event.ArchiveID != id {
		return fmt.Sprintf("第 %d 条事件的档案编号不匹配", line)
	}
	if event.PreviousHash != previous {
		return fmt.Sprintf("第 %d 条事件的 previousHash 与前一事件不匹配", line)
	}
	if expected := hashEvent(event); event.Hash != expected {
		return fmt.Sprintf("第 %d 条事件哈希异常", line)
	}
	return ""
}

func (s *JSONStore) readAudit(id string) ([]archive.AuditEvent, error) {
	file, err := os.Open(s.auditPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return []archive.AuditEvent{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("打开审计日志: %w", err)
	}
	defer file.Close()
	result := []archive.AuditEvent{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	line := 0
	previous := ""
	for scanner.Scan() {
		line++
		var event archive.AuditEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("第 %d 行 JSON 无效: %w", line, err)
		}
		if event.Sequence != int64(line) {
			return nil, fmt.Errorf("第 %d 行序号为 %d", line, event.Sequence)
		}
		if event.ArchiveVersion != int64(line) {
			return nil, fmt.Errorf("第 %d 行档案版本为 %d，无法连续重放", line, event.ArchiveVersion)
		}
		if event.ArchiveID != id {
			return nil, fmt.Errorf("第 %d 行档案编号不匹配", line)
		}
		if event.PreviousHash != previous {
			return nil, fmt.Errorf("第 %d 行前序摘要不匹配", line)
		}
		if expected := hashEvent(event); event.Hash != expected {
			return nil, fmt.Errorf("第 %d 行事件摘要不匹配", line)
		}
		previous = event.Hash
		result = append(result, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取审计日志第 %d 行附近: %w", line+1, err)
	}
	return result, nil
}

func (s *JSONStore) FindAction(id, key string) (archive.AuditEvent, bool, error) {
	if key == "" {
		return archive.AuditEvent{}, false, nil
	}
	events, err := s.Audit(id)
	if err != nil {
		return archive.AuditEvent{}, false, err
	}
	for _, event := range events {
		if event.ActionKey == key {
			return event, true, nil
		}
	}
	return archive.AuditEvent{}, false, nil
}

func DecodeArchiveResult(event archive.AuditEvent) (*archive.InterviewArchive, error) {
	if len(event.Result) == 0 {
		return nil, errors.New("审计事件没有保存稳定结果")
	}
	var value archive.InterviewArchive
	if err := json.Unmarshal(event.Result, &value); err != nil {
		return nil, fmt.Errorf("解析审计事件稳定结果: %w", err)
	}
	if value.ID != event.ArchiveID || value.Version != event.ArchiveVersion {
		return nil, errors.New("审计事件稳定结果与事件元数据不匹配")
	}
	value.Normalize()
	return &value, nil
}

func (s *JSONStore) SaveArtifact(archiveID, name string, data []byte) error {
	if err := validateID(archiveID); err != nil {
		return err
	}
	if err := validateID(name); err != nil {
		return err
	}
	if len(data) == 0 {
		return errors.New("制品内容不能为空")
	}
	lock := s.archiveLock(archiveID)
	lock.Lock()
	defer lock.Unlock()
	return atomicWrite(filepath.Join(s.artifacts, archiveID+"--"+name+".json"), data, 0o640)
}

func (s *JSONStore) LoadArtifact(archiveID, name string) ([]byte, error) {
	if err := validateID(archiveID); err != nil {
		return nil, err
	}
	if err := validateID(name); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.artifacts, archiveID+"--"+name+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, archive.ErrNotFound
	}
	return data, err
}

func (s *JSONStore) writeSnapshot(value *archive.InterviewArchive) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("编码档案快照: %w", err)
	}
	data = append(data, '\n')
	if err := atomicWrite(s.snapshotPath(value.ID), data, 0o640); err != nil {
		return fmt.Errorf("原子保存档案快照: %w", err)
	}
	return nil
}

func (s *JSONStore) appendAudit(event archive.AuditEvent) error {
	file, err := os.OpenFile(s.auditPath(event.ArchiveID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("打开审计日志: %w", err)
	}
	data, err := json.Marshal(event)
	if err == nil {
		_, err = file.Write(append(data, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("追加审计日志: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭审计日志: %w", closeErr)
	}
	return syncDirectory(s.audits)
}

func hashEvent(event archive.AuditEvent) string {
	payload := struct {
		Sequence       int64     `json:"sequence"`
		ArchiveID      string    `json:"archiveId"`
		At             time.Time `json:"at"`
		Actor          string    `json:"actor"`
		Action         string    `json:"action"`
		Detail         string    `json:"detail"`
		ArchiveVersion int64     `json:"archiveVersion"`
		ActionKey      string    `json:"actionKey,omitempty"`
		Result         []byte    `json:"result,omitempty"`
		PreviousHash   string    `json:"previousHash"`
	}{event.Sequence, event.ArchiveID, event.At.UTC(), event.Actor, event.Action, event.Detail, event.ArchiveVersion, event.ActionKey, event.Result, event.PreviousHash}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func cloneArchive(value *archive.InterviewArchive) *archive.InterviewArchive {
	data, _ := json.Marshal(value)
	var out archive.InterviewArchive
	_ = json.Unmarshal(data, &out)
	out.Normalize()
	return &out
}
