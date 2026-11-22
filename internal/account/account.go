// Package account 管理账户和固定组（Owner/Admin/User）。
package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gitlab.com/elegantgx/chat-deepseek/internal/auth"
	"gitlab.com/elegantgx/chat-deepseek/internal/id"
)

// Group 是固定的账户组名，不带复数 s。
type Group string

const (
	GroupUser  Group = "User"
	GroupAdmin Group = "Admin"
	GroupOwner Group = "Owner"
)

var groupRanks = map[Group]int{
	GroupUser:  1,
	GroupAdmin: 2,
	GroupOwner: 3,
}

// AllGroups 按能力从低到高返回固定组。
func AllGroups() []Group {
	return []Group{GroupUser, GroupAdmin, GroupOwner}
}

// ParseGroup 解析用户输入的组名，大小写不敏感，返回规范名称。
func ParseGroup(value string) (Group, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "user":
		return GroupUser, true
	case "admin":
		return GroupAdmin, true
	case "owner":
		return GroupOwner, true
	default:
		return "", false
	}
}

// Rank 返回组的权限级别；未知组为 0。
func (g Group) Rank() int {
	return groupRanks[g]
}

// User 是可对外展示的账户信息，不包含密码哈希。
type User struct {
	ID        string  `json:"id"`
	Username  string  `json:"username"`
	Groups    []Group `json:"-"`
	Disabled  bool    `json:"disabled"`
	CreatedAt int64   `json:"createdAt"`
	UpdatedAt int64   `json:"updatedAt"`
}

// HasGroup 判断用户是否属于指定组。
func (u *User) HasGroup(group Group) bool {
	for _, item := range u.Groups {
		if item == group {
			return true
		}
	}
	return false
}

var (
	ErrUserNotFound  = errors.New("account: user not found")
	ErrUsernameTaken = errors.New("account: username already exists")
	ErrInvalidUser   = errors.New("account: invalid username")
	ErrInvalidGroup  = errors.New("account: invalid group")
	ErrBaseGroup     = errors.New("account: cannot remove the base User group")
	ErrLastOwner     = errors.New("account: cannot remove the last active Owner")
	ErrEmptyPassword = errors.New("account: password hash is empty")
	ErrInvalidUserID = errors.New("account: user id is empty")
)

// Store 封装账户与组关系的数据库操作。
type Store struct {
	db *sql.DB
}

// NewStore 创建账户存储。
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create 创建账户；User 组会自动加入，其他组按传入顺序去重。
func (s *Store) Create(
	ctx context.Context,
	username string,
	passwordHash string,
	groups []Group,
) (*User, error) {
	username = strings.TrimSpace(username)
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if passwordHash == "" {
		return nil, ErrEmptyPassword
	}
	normalized, err := normalizeGroups(groups)
	if err != nil {
		return nil, err
	}

	userID, err := id.New()
	if err != nil {
		return nil, fmt.Errorf("generate user id: %w", err)
	}
	now := time.Now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin create user: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO users (id, username, password_hash, disabled, created_at, updated_at)
		 VALUES (?, ?, ?, 0, ?, ?)`,
		userID,
		username,
		passwordHash,
		now,
		now,
	); err != nil {
		if isUniqueUsername(err) {
			return nil, ErrUsernameTaken
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}
	for _, group := range normalized {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO user_groups (user_id, group_name) VALUES (?, ?)`,
			userID,
			group,
		); err != nil {
			return nil, fmt.Errorf("insert group %s: %w", group, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit create user: %w", err)
	}
	return s.GetByID(ctx, userID)
}

// EnsureOwner 在没有任何活动 Owner 时创建一个 Owner 账户。
func (s *Store) EnsureOwner(ctx context.Context, username, passwordHash string) (*User, error) {
	count, err := s.CountActiveOwners(ctx)
	if err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, ErrLastOwner
	}
	return s.Create(ctx, username, passwordHash, []Group{GroupUser, GroupOwner})
}

// GetByID 按 ID 读取账户。
func (s *Store) GetByID(ctx context.Context, userID string) (*User, error) {
	if userID == "" {
		return nil, ErrInvalidUserID
	}
	return s.getOne(ctx, `WHERE id = ?`, userID)
}

// GetByUsername 按用户名读取账户（大小写不敏感）。
func (s *Store) GetByUsername(ctx context.Context, username string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, ErrUserNotFound
	}
	return s.getOne(ctx, `WHERE username = ?`, username)
}

func (s *Store) getOne(ctx context.Context, where string, arg any) (*User, error) {
	var user User
	var disabled int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id, username, disabled, created_at, updated_at FROM users `+where,
		arg,
	).Scan(&user.ID, &user.Username, &disabled, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query user: %w", err)
	}
	user.Disabled = disabled != 0
	groups, err := s.Groups(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	user.Groups = groups
	return &user, nil
}

// List 按用户名返回全部账户。
func (s *Store) List(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, username, disabled, created_at, updated_at FROM users ORDER BY username COLLATE NOCASE`,
	)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var users []*User
	for rows.Next() {
		var user User
		var disabled int
		if err := rows.Scan(&user.ID, &user.Username, &disabled, &user.CreatedAt, &user.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		user.Disabled = disabled != 0
		users = append(users, &user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	for _, user := range users {
		groups, err := s.Groups(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		user.Groups = groups
	}
	return users, nil
}

// Groups 返回账户的组，按权限从低到高排序。
func (s *Store) Groups(ctx context.Context, userID string) ([]Group, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT g.name
		   FROM user_groups ug
		   JOIN groups g ON g.name = ug.group_name
		  WHERE ug.user_id = ?
		  ORDER BY g.rank`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("query user groups: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var groups []Group
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan user group: %w", err)
		}
		groups = append(groups, Group(name))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user groups: %w", err)
	}
	return groups, nil
}

// VerifyPassword 校验账户当前密码。
func (s *Store) VerifyPassword(ctx context.Context, userID, password string) (bool, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrUserNotFound
	}
	if err != nil {
		return false, fmt.Errorf("query password hash: %w", err)
	}
	return auth.VerifyPassword(hash, password)
}

// SetPassword 更新密码哈希。
func (s *Store) SetPassword(ctx context.Context, userID, passwordHash string) error {
	if passwordHash == "" {
		return ErrEmptyPassword
	}
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash,
		time.Now().UnixMilli(),
		userID,
	)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return requireAffected(result, ErrUserNotFound)
}

// SetDisabled 禁用/启用账户；禁用会吊销全部登录会话。
func (s *Store) SetDisabled(ctx context.Context, userID string, disabled bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin disable user: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if disabled {
		last, err := isLastActiveOwner(ctx, tx, userID)
		if err != nil {
			return err
		}
		if last {
			return ErrLastOwner
		}
	}
	value := 0
	if disabled {
		value = 1
	}
	result, err := tx.ExecContext(
		ctx,
		`UPDATE users SET disabled = ?, updated_at = ? WHERE id = ?`,
		value,
		time.Now().UnixMilli(),
		userID,
	)
	if err != nil {
		return fmt.Errorf("update disabled: %w", err)
	}
	if err := requireAffected(result, ErrUserNotFound); err != nil {
		return err
	}
	if disabled {
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("revoke sessions: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit disable user: %w", err)
	}
	return nil
}

// AddGroup 给账户添加附加组；User 组始终保留。
func (s *Store) AddGroup(ctx context.Context, userID string, group Group) error {
	if group.Rank() == 0 {
		return ErrInvalidGroup
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin add group: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureUserExists(ctx, tx, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO user_groups (user_id, group_name) VALUES (?, ?)`,
		userID,
		GroupUser,
	); err != nil {
		return fmt.Errorf("ensure base group: %w", err)
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO user_groups (user_id, group_name) VALUES (?, ?)`,
		userID,
		group,
	); err != nil {
		return fmt.Errorf("add group: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit add group: %w", err)
	}
	return nil
}

// RemoveGroup 移除附加组；不能移除 User，也不能移除最后一名活动 Owner 的 Owner 组。
func (s *Store) RemoveGroup(ctx context.Context, userID string, group Group) error {
	if group == GroupUser {
		return ErrBaseGroup
	}
	if group.Rank() == 0 {
		return ErrInvalidGroup
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin remove group: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureUserExists(ctx, tx, userID); err != nil {
		return err
	}
	if group == GroupOwner {
		last, err := isLastActiveOwner(ctx, tx, userID)
		if err != nil {
			return err
		}
		if last {
			return ErrLastOwner
		}
	}
	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM user_groups WHERE user_id = ? AND group_name = ?`,
		userID,
		group,
	); err != nil {
		return fmt.Errorf("remove group: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit remove group: %w", err)
	}
	return nil
}

// Delete 删除账户及其组关系/会话。最后一名活动 Owner 不允许删除。
func (s *Store) Delete(ctx context.Context, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete user: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	last, err := isLastActiveOwner(ctx, tx, userID)
	if err != nil {
		return err
	}
	if last {
		return ErrLastOwner
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if err := requireAffected(result, ErrUserNotFound); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete user: %w", err)
	}
	return nil
}

// CountActiveOwners 返回未禁用的 Owner 数量。
func (s *Store) CountActiveOwners(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		   FROM user_groups ug
		   JOIN users u ON u.id = ug.user_id
		  WHERE ug.group_name = ? AND u.disabled = 0`,
		GroupOwner,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count owners: %w", err)
	}
	return count, nil
}

// ValidateUsername 校验用户名：3-64 个 ASCII 字母、数字或 . _ -。
func ValidateUsername(username string) error {
	if len(username) < 3 || len(username) > 64 {
		return ErrInvalidUser
	}
	for _, char := range username {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '.', char == '_', char == '-':
		default:
			return ErrInvalidUser
		}
	}
	return nil
}

func normalizeGroups(groups []Group) ([]Group, error) {
	seen := make(map[Group]struct{}, len(groups)+1)
	normalized := make([]Group, 0, len(groups)+1)
	for _, group := range append([]Group{GroupUser}, groups...) {
		if group.Rank() == 0 {
			return nil, ErrInvalidGroup
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		normalized = append(normalized, group)
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].Rank() < normalized[j].Rank()
	})
	return normalized, nil
}

func isLastActiveOwner(ctx context.Context, tx *sql.Tx, userID string) (bool, error) {
	var disabled int
	if err := tx.QueryRowContext(ctx, `SELECT disabled FROM users WHERE id = ?`, userID).Scan(&disabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrUserNotFound
		}
		return false, fmt.Errorf("check user disabled: %w", err)
	}
	// 已禁用的账户不再算“活动 Owner”，移除/删除它不会减少可用 Owner。
	if disabled != 0 {
		return false, nil
	}
	var isOwner int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM user_groups WHERE user_id = ? AND group_name = ?`,
		userID,
		GroupOwner,
	).Scan(&isOwner); err != nil {
		return false, fmt.Errorf("check owner group: %w", err)
	}
	if isOwner == 0 {
		return false, nil
	}
	var others int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		   FROM user_groups ug
		   JOIN users u ON u.id = ug.user_id
		  WHERE ug.group_name = ? AND u.disabled = 0 AND u.id <> ?`,
		GroupOwner,
		userID,
	).Scan(&others); err != nil {
		return false, fmt.Errorf("count other owners: %w", err)
	}
	return others == 0, nil
}

func ensureUserExists(ctx context.Context, tx *sql.Tx, userID string) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = ?`, userID).Scan(&exists); err != nil {
		return fmt.Errorf("check user: %w", err)
	}
	if exists == 0 {
		return ErrUserNotFound
	}
	return nil
}

func requireAffected(result sql.Result, notFound error) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return notFound
	}
	return nil
}

func isUniqueUsername(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed: users.username")
}
