package channelcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strconv"
	"strings"

	"controltower/agent/internal/fileatomic"
)

type UpdateRequest struct {
	ChannelID int64
	Status    *int
	Weight    *uint
	Priority  *int64
}

type Result struct {
	ChannelID int64
	Status    *int
	Weight    *uint
	Priority  *int64
}

type TokenStore interface {
	Load() (string, error)
	Save(token string) error
}

type FileTokenStore struct{ path string }

func NewFileTokenStore(path string) FileTokenStore { return FileTokenStore{path: path} }

func (s FileTokenStore) Load() (string, error) {
	if s.path == "" {
		return "", nil
	}
	data, err := fileatomic.ReadFile(s.path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (s FileTokenStore) Save(token string) error {
	if s.path == "" {
		return nil
	}
	return fileatomic.WriteFile(s.path, []byte(strings.TrimSpace(token)+"\n"), 0600)
}

type Client struct {
	baseURL     string
	accessToken string
	username    string
	password    string
	adminUserID int64
	tokenStore  TokenStore
	httpClient  *http.Client
}

func New(baseURL, accessToken string, adminUserID int64, httpClient *http.Client) *Client {
	return NewWithCredentials(baseURL, accessToken, "", "", adminUserID, nil, httpClient)
}

func NewWithCredentials(baseURL, accessToken, username, password string, adminUserID int64, tokenStore TokenStore, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	if httpClient.Jar == nil {
		if jar, err := cookiejar.New(nil); err == nil {
			httpClient.Jar = jar
		}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), accessToken: accessToken, username: username, password: password, adminUserID: adminUserID, tokenStore: tokenStore, httpClient: httpClient}
}

func (c *Client) Update(ctx context.Context, update UpdateRequest) (Result, error) {
	if update.ChannelID <= 0 {
		return Result{}, fmt.Errorf("channel id must be positive")
	}
	if err := c.ensureToken(ctx); err != nil {
		return Result{}, err
	}
	if c.adminUserID <= 0 {
		return Result{}, fmt.Errorf("new-api admin user id is not configured")
	}

	channel, err := c.get(ctx, update.ChannelID)
	if err != nil {
		return Result{}, err
	}
	delete(channel, "key")
	if update.Status != nil {
		channel["status"] = *update.Status
	}
	if update.Weight != nil {
		channel["weight"] = *update.Weight
	}
	if update.Priority != nil {
		channel["priority"] = *update.Priority
	}
	channel["id"] = update.ChannelID

	body, err := json.Marshal(channel)
	if err != nil {
		return Result{}, err
	}
	var response apiResponse
	if err := c.doJSON(ctx, http.MethodPut, c.baseURL+"/api/channel/", body, &response); err != nil {
		return Result{}, err
	}
	if !response.Success {
		return Result{}, fmt.Errorf("new-api channel update failed: %s", response.Message)
	}
	return Result{ChannelID: update.ChannelID, Status: intPointer(channelNumber(channel["status"])), Weight: uintPointer(channelNumber(channel["weight"])), Priority: int64Pointer(channelNumber(channel["priority"]))}, nil
}

func (c *Client) ensureToken(ctx context.Context) error {
	if c.accessToken != "" {
		return nil
	}
	if c.tokenStore != nil {
		token, err := c.tokenStore.Load()
		if err != nil {
			return fmt.Errorf("load new-api access token: %w", err)
		}
		if token != "" {
			c.accessToken = token
			return nil
		}
	}
	if c.username == "" || c.password == "" {
		return fmt.Errorf("new-api admin credentials are not configured")
	}

	loginBody, err := json.Marshal(map[string]string{"username": c.username, "password": c.password})
	if err != nil {
		return err
	}
	var login loginResponse
	if err := c.doUnauthenticated(ctx, http.MethodPost, c.baseURL+"/api/user/login", loginBody, &login); err != nil {
		return fmt.Errorf("new-api admin login failed: %w", err)
	}
	if !login.Success {
		return fmt.Errorf("new-api admin login failed: %s", login.Message)
	}
	if login.Require2FA || login.Data.Require2FA {
		return fmt.Errorf("new-api admin login requires 2fa; configure CT_NEW_API_ADMIN_ACCESS_TOKEN instead")
	}

	var token tokenResponse
	if err := c.doUnauthenticated(ctx, http.MethodGet, c.baseURL+"/api/user/self/token", nil, &token); err != nil {
		return fmt.Errorf("new-api access token request failed: %w", err)
	}
	if !token.Success || strings.TrimSpace(token.Data) == "" {
		return fmt.Errorf("new-api access token request failed: %s", token.Message)
	}
	c.accessToken = strings.TrimSpace(token.Data)
	if c.tokenStore != nil {
		if err := c.tokenStore.Save(c.accessToken); err != nil {
			return fmt.Errorf("save new-api access token: %w", err)
		}
	}
	return nil
}

func (c *Client) get(ctx context.Context, channelID int64) (map[string]any, error) {
	var response apiResponse
	if err := c.doJSON(ctx, http.MethodGet, c.baseURL+"/api/channel/"+strconv.FormatInt(channelID, 10), nil, &response); err != nil {
		return nil, err
	}
	if !response.Success || response.Data == nil {
		return nil, fmt.Errorf("new-api channel lookup failed: %s", response.Message)
	}
	return response.Data, nil
}

type apiResponse struct {
	Success bool           `json:"success"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
}

type loginResponse struct {
	Success    bool   `json:"success"`
	Message    string `json:"message"`
	Require2FA bool   `json:"require_2fa"`
	Data       struct {
		Require2FA bool `json:"require_2fa"`
	} `json:"data"`
}

type tokenResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

func (c *Client) doJSON(ctx context.Context, method, url string, body []byte, target *apiResponse) error {
	return c.do(ctx, method, url, body, target, true)
}

func (c *Client) doUnauthenticated(ctx context.Context, method, url string, body []byte, target any) error {
	return c.do(ctx, method, url, body, target, false)
}

func (c *Client) do(ctx context.Context, method, url string, body []byte, target any, authenticated bool) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	if authenticated {
		req.Header.Set("Authorization", "Bearer "+c.accessToken)
		req.Header.Set("New-Api-User", strconv.FormatInt(c.adminUserID, 10))
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("new-api request failed with status %d", resp.StatusCode)
	}
	if target == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func channelNumber(value any) int64 {
	switch number := value.(type) {
	case float64:
		return int64(number)
	case int:
		return int64(number)
	case uint:
		return int64(number)
	case int64:
		return number
	default:
		return 0
	}
}

func intPointer(value int64) *int     { result := int(value); return &result }
func uintPointer(value int64) *uint   { result := uint(value); return &result }
func int64Pointer(value int64) *int64 { return &value }
