package dapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// User contains only non-secret local-user profile fields. Passwords and roles
// are deliberately absent so reads and profile updates never expose or reset
// credentials.
type User struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	FirstName string          `json:"firstName"`
	LastName  string          `json:"lastName"`
	Email     string          `json:"email"`
	Tag       string          `json:"tag"`
	Active    bool            `json:"active,omitempty"`
	Type      string          `json:"@type,omitempty"`
	Source    string          `json:"source,omitempty"`
	Roles     json.RawMessage `json:"roles,omitempty"`
}

// CreateLocalUser creates one local Community Edition user. The password is
// deliberately an argument rather than a User field, so it cannot be returned
// by reads or accidentally reused in profile updates.
func (c *Client) CreateLocalUser(user User, password string) (*User, error) {
	if user.Name == "" || password == "" {
		return nil, fmt.Errorf("local user name and password are required")
	}
	result := new(User)
	body, err := json.Marshal(struct {
		Name      string `json:"name"`
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
		Email     string `json:"email"`
		Password  string `json:"password"`
	}{user.Name, user.FirstName, user.LastName, user.Email, password})
	if err != nil {
		return nil, err
	}
	if err := c.request(http.MethodPost, "/api/v3/user", bytes.NewBuffer(body), result); err != nil {
		return nil, err
	}
	if result.ID == "" || result.Name != user.Name {
		return nil, fmt.Errorf("invalid created user response for %s", user.Name)
	}
	if result.Type == "EnterpriseUser" || result.Source == "external" || len(result.Roles) > 0 {
		return nil, fmt.Errorf("created user %s is not a supported local Community Edition user", user.Name)
	}
	return result, nil
}

// GetUser reads a user by its stable UUID; Community Edition supports this
// endpoint even though GET /api/v3/user (listing) is Enterprise-only.
func (c *Client) GetUser(id string) (*User, error) {
	result := new(User)
	if err := c.request(http.MethodGet, "/api/v3/user/"+url.PathEscape(id), nil, result); err != nil {
		return nil, err
	}
	if result.ID != id || result.Name == "" {
		return nil, fmt.Errorf("invalid user response for id %s", id)
	}
	if result.Type == "EnterpriseUser" || result.Source == "external" || len(result.Roles) > 0 {
		return nil, fmt.Errorf("user %s is not a supported local Community Edition user; refusing to manage Enterprise or externally provisioned accounts", id)
	}
	return result, nil
}

// UpdateUserProfile preserves the current version for optimistic concurrency
// and sends only profile attributes. It never sends a password, roles or active.
func (c *Client) UpdateUserProfile(user User) error {
	body, err := json.Marshal(struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
		Email     string `json:"email"`
		Tag       string `json:"tag"`
	}{user.ID, user.Name, user.FirstName, user.LastName, user.Email, user.Tag})
	if err != nil {
		return err
	}
	return c.request(http.MethodPut, "/api/v3/user/"+url.PathEscape(user.ID), bytes.NewBuffer(body), nil)
}
