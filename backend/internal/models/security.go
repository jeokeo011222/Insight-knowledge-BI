// Package models holds the GORM models for all superset tables. Table and
// column names match the Python source 1:1.
package models

// FAB security models (flask_appbuilder.security.sqla.models) plus the
// superset-specific ab_group family.

import "time"

// User maps the ab_user table (FAB User model).
type User struct {
	BaseModel
	FirstName      string     `gorm:"column:first_name;not null" json:"first_name"`
	LastName       string     `gorm:"column:last_name;not null" json:"last_name"`
	Username       string     `gorm:"column:username;unique;not null" json:"username"`
	Password       string     `gorm:"column:password" json:"-"`
	Active         bool       `gorm:"column:active" json:"active"`
	Email          string     `gorm:"column:email;unique;not null" json:"email"`
	LastLogin      *time.Time `gorm:"column:last_login" json:"last_login"`
	LoginCount     *int       `gorm:"column:login_count" json:"login_count"`
	FailLoginCount *int       `gorm:"column:fail_login_count" json:"fail_login_count"`
}

// TableName returns the original table name.
func (User) TableName() string { return "ab_user" }

// Role maps the ab_role table (FAB Role model).
type Role struct {
	ID   int    `gorm:"column:id;primaryKey" json:"id"`
	Name string `gorm:"column:name;unique;not null" json:"name"`
}

// TableName returns the original table name.
func (Role) TableName() string { return "ab_role" }

// Permission maps the ab_permission table (FAB Permission model).
type Permission struct {
	ID   int    `gorm:"column:id;primaryKey" json:"id"`
	Name string `gorm:"column:name;unique;not null" json:"name"`
}

// TableName returns the original table name.
func (Permission) TableName() string { return "ab_permission" }

// ViewMenu maps the ab_view_menu table (FAB ViewMenu model).
type ViewMenu struct {
	ID   int    `gorm:"column:id;primaryKey" json:"id"`
	Name string `gorm:"column:name;unique;not null" json:"name"`
}

// TableName returns the original table name.
func (ViewMenu) TableName() string { return "ab_view_menu" }

// PermissionView maps the ab_permission_view table (FAB PermissionView model).
type PermissionView struct {
	ID           int `gorm:"column:id;primaryKey" json:"id"`
	PermissionID int `gorm:"column:permission_id;uniqueIndex:uq_permission_view" json:"permission_id"`
	ViewMenuID   int `gorm:"column:view_menu_id;uniqueIndex:uq_permission_view" json:"view_menu_id"`
}

// TableName returns the original table name.
func (PermissionView) TableName() string { return "ab_permission_view" }

// PermissionViewRole maps the ab_permission_view_role association table.
type PermissionViewRole struct {
	ID               int `gorm:"column:id;primaryKey" json:"id"`
	PermissionViewID int `gorm:"column:permission_view_id;uniqueIndex:uq_permission_view_role" json:"permission_view_id"`
	RoleID           int `gorm:"column:role_id;uniqueIndex:uq_permission_view_role" json:"role_id"`
}

// TableName returns the original table name.
func (PermissionViewRole) TableName() string { return "ab_permission_view_role" }

// UserRole maps the ab_user_role association table.
type UserRole struct {
	ID     int `gorm:"column:id;primaryKey" json:"id"`
	UserID int `gorm:"column:user_id;uniqueIndex:uq_user_role" json:"user_id"`
	RoleID int `gorm:"column:role_id;uniqueIndex:uq_user_role" json:"role_id"`
}

// TableName returns the original table name.
func (UserRole) TableName() string { return "ab_user_role" }

// Group maps the ab_group table (FAB Group model).
type Group struct {
	ID          int    `gorm:"column:id;primaryKey" json:"id"`
	Name        string `gorm:"column:name;unique;not null" json:"name"`
	Label       string `gorm:"column:label" json:"label"`
	Description string `gorm:"column:description" json:"description"`
}

// TableName returns the original table name.
func (Group) TableName() string { return "ab_group" }

// UserGroup maps the ab_user_group association table.
type UserGroup struct {
	ID      int `gorm:"column:id;primaryKey" json:"id"`
	UserID  int `gorm:"column:user_id;uniqueIndex:uq_user_group" json:"user_id"`
	GroupID int `gorm:"column:group_id;uniqueIndex:uq_user_group" json:"group_id"`
}

// TableName returns the original table name.
func (UserGroup) TableName() string { return "ab_user_group" }

// GroupRole maps the ab_group_role association table.
type GroupRole struct {
	ID      int `gorm:"column:id;primaryKey" json:"id"`
	GroupID int `gorm:"column:group_id;uniqueIndex:uq_group_role" json:"group_id"`
	RoleID  int `gorm:"column:role_id;uniqueIndex:uq_group_role" json:"role_id"`
}

// TableName returns the original table name.
func (GroupRole) TableName() string { return "ab_group_role" }

// RegisterUser maps the ab_register_user table (FAB RegisterUser model).
type RegisterUser struct {
	ID               int        `gorm:"column:id;primaryKey" json:"id"`
	FirstName        string     `gorm:"column:first_name;not null" json:"first_name"`
	LastName         string     `gorm:"column:last_name;not null" json:"last_name"`
	Username         string     `gorm:"column:username;unique;not null" json:"username"`
	Password         string     `gorm:"column:password" json:"-"`
	Email            string     `gorm:"column:email;not null" json:"email"`
	RegistrationDate *time.Time `gorm:"column:registration_date" json:"registration_date"`
	RegistrationHash string     `gorm:"column:registration_hash" json:"registration_hash"`
}

// TableName returns the original table name.
func (RegisterUser) TableName() string { return "ab_register_user" }
