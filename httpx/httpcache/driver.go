// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpcache

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/bartventer/httpcache/store"
	"github.com/bartventer/httpcache/store/driver"

	"github.com/golusoris/golusoris/cache/twotier"
)

const driverScheme = "golusoris-httpcache"

var (
	driverOnce sync.Once
	instances  sync.Map
	errMiss    = errors.New("httpcache: cache miss")
)

func registerDriver() {
	driverOnce.Do(func() { store.Register(driverScheme, driver.DriverFunc(openConnection)) })
}

func openConnection(u *url.URL) (driver.Conn, error) {
	conn, ok := instances.Load(u.Host)
	if !ok {
		return nil, errors.New("httpcache: unknown cache instance")
	}
	adapter, ok := conn.(*connection)
	if !ok {
		return nil, errors.New("httpcache: invalid cache instance")
	}
	return adapter, nil
}

type connection struct {
	view    *twotier.Typed[[]byte]
	timeout time.Duration
}

func (c *connection) Get(key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	value, err := c.view.Get(ctx, key, func(context.Context) ([]byte, error) { return nil, errMiss })
	if errors.Is(err, errMiss) {
		return nil, fmt.Errorf("httpcache: get: %w", driver.ErrNotExist)
	}
	if err != nil {
		return nil, fmt.Errorf("httpcache: get: %w", err)
	}
	return value, nil
}

func (c *connection) Set(key string, value []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	if err := c.view.Set(ctx, key, value); err != nil {
		return fmt.Errorf("httpcache: set: %w", err)
	}
	return nil
}

func (c *connection) Delete(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	if err := c.view.Delete(ctx, key); err != nil {
		return fmt.Errorf("httpcache: delete: %w", err)
	}
	return nil
}
