package scim_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/scim"
)

func TestUsers_CreateGetDelete(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(scim.Handler(newMemStore()))
	t.Cleanup(srv.Close)

	body := bytes.NewBufferString(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"alice","active":true}`)
	resp, err := http.Post(srv.URL+"/Users", "application/scim+json", body)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var u scim.User
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&u))
	require.NoError(t, resp.Body.Close())
	require.NotEmpty(t, u.ID)
	require.Equal(t, "alice", u.UserName)

	// GET
	resp2, err := http.Get(srv.URL + "/Users/" + u.ID)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	require.NoError(t, resp2.Body.Close())

	// DELETE
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/Users/"+u.ID, nil)
	resp3, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp3.StatusCode)
	require.NoError(t, resp3.Body.Close())

	// 404 after delete
	resp4, err := http.Get(srv.URL + "/Users/" + u.ID)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp4.StatusCode)
	require.NoError(t, resp4.Body.Close())
}

func TestUsers_List(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	_, _ = store.CreateUser(context.Background(), scim.User{UserName: "a"})
	_, _ = store.CreateUser(context.Background(), scim.User{UserName: "b"})

	srv := httptest.NewServer(scim.Handler(store))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/Users")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var lr scim.ListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&lr))
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 2, lr.TotalResults)
}

// --- in-memory store ---

type memStore struct {
	mu     sync.Mutex
	users  map[string]scim.User
	groups map[string]scim.Group
	seq    int
}

func newMemStore() *memStore {
	return &memStore{users: map[string]scim.User{}, groups: map[string]scim.Group{}}
}

func (m *memStore) nextID() string {
	m.seq++
	return "id-" + itoa(m.seq)
}

func (m *memStore) CreateUser(_ context.Context, u scim.User) (scim.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u.ID = m.nextID()
	m.users[u.ID] = u
	return u, nil
}

func (m *memStore) GetUser(_ context.Context, id string) (scim.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return scim.User{}, scim.ErrNotFound
	}
	return u, nil
}

func (m *memStore) ListUsers(_ context.Context, _, _ int, _ string) ([]scim.User, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]scim.User, 0, len(m.users))
	for _, u := range m.users {
		out = append(out, u)
	}
	return out, len(out), nil
}

func (m *memStore) UpdateUser(_ context.Context, u scim.User) (scim.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[u.ID]; !ok {
		return scim.User{}, scim.ErrNotFound
	}
	m.users[u.ID] = u
	return u, nil
}

func (m *memStore) DeleteUser(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[id]; !ok {
		return scim.ErrNotFound
	}
	delete(m.users, id)
	return nil
}

func (m *memStore) CreateGroup(_ context.Context, g scim.Group) (scim.Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g.ID = m.nextID()
	m.groups[g.ID] = g
	return g, nil
}

func (m *memStore) GetGroup(_ context.Context, id string) (scim.Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[id]
	if !ok {
		return scim.Group{}, scim.ErrNotFound
	}
	return g, nil
}

func (m *memStore) ListGroups(_ context.Context, _, _ int, _ string) ([]scim.Group, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]scim.Group, 0, len(m.groups))
	for _, g := range m.groups {
		out = append(out, g)
	}
	return out, len(out), nil
}

func (m *memStore) UpdateGroup(_ context.Context, g scim.Group) (scim.Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.groups[g.ID]; !ok {
		return scim.Group{}, scim.ErrNotFound
	}
	m.groups[g.ID] = g
	return g, nil
}

func (m *memStore) DeleteGroup(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.groups[id]; !ok {
		return scim.ErrNotFound
	}
	delete(m.groups, id)
	return nil
}

func TestGroups_CreateGetUpdateDelete(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(scim.Handler(newMemStore()))
	t.Cleanup(srv.Close)

	body := bytes.NewBufferString(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],"displayName":"eng"}`)
	resp, err := http.Post(srv.URL+"/Groups", "application/scim+json", body)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var g scim.Group
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&g))
	require.NoError(t, resp.Body.Close())
	require.NotEmpty(t, g.ID)
	require.Equal(t, "eng", g.DisplayName)

	// GET
	resp2, err := http.Get(srv.URL + "/Groups/" + g.ID)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	require.NoError(t, resp2.Body.Close())

	// PUT update
	upd := bytes.NewBufferString(`{"displayName":"platform"}`)
	reqPut, _ := http.NewRequest(http.MethodPut, srv.URL+"/Groups/"+g.ID, upd)
	reqPut.Header.Set("Content-Type", "application/scim+json")
	resp3, err := http.DefaultClient.Do(reqPut)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp3.StatusCode)
	require.NoError(t, resp3.Body.Close())

	// DELETE
	reqDel, _ := http.NewRequest(http.MethodDelete, srv.URL+"/Groups/"+g.ID, nil)
	resp4, err := http.DefaultClient.Do(reqDel)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp4.StatusCode)
	require.NoError(t, resp4.Body.Close())

	// 404 after delete
	resp5, err := http.Get(srv.URL + "/Groups/" + g.ID)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, resp5.StatusCode)
	require.NoError(t, resp5.Body.Close())
}

func TestGroups_List(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	_, _ = store.CreateGroup(context.Background(), scim.Group{DisplayName: "a"})
	_, _ = store.CreateGroup(context.Background(), scim.Group{DisplayName: "b"})

	srv := httptest.NewServer(scim.Handler(store))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/Groups")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var lr scim.ListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&lr))
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 2, lr.TotalResults)
}

func TestUsers_Update(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	u, _ := store.CreateUser(context.Background(), scim.User{UserName: "alice"})

	srv := httptest.NewServer(scim.Handler(store))
	t.Cleanup(srv.Close)

	body := bytes.NewBufferString(`{"userName":"alice-updated","active":false}`)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/Users/"+u.ID, body)
	req.Header.Set("Content-Type", "application/scim+json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got scim.User
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.NoError(t, resp.Body.Close())
	require.Equal(t, "alice-updated", got.UserName)
}

func TestUsers_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(scim.Handler(newMemStore()))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/Users", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

func TestGroups_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(scim.Handler(newMemStore()))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/Groups", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

func TestGroupItem_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	g, _ := store.CreateGroup(context.Background(), scim.Group{DisplayName: "x"})
	srv := httptest.NewServer(scim.Handler(store))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/Groups/"+g.ID, nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

func TestUserItem_InvalidPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(scim.Handler(newMemStore()))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/Users/")
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

func TestGroupItem_InvalidPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(scim.Handler(newMemStore()))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/Groups/")
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

func TestPaging_QueryParams(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	for range 5 {
		_, _ = store.CreateUser(context.Background(), scim.User{UserName: "u"})
	}

	srv := httptest.NewServer(scim.Handler(store))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/Users?startIndex=1&count=3")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var lr scim.ListResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&lr))
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 5, lr.TotalResults)
}

// itoa avoids strconv import in the test file.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[n:])
}
