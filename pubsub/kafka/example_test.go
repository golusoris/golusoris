// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package kafka_test

import (
	"fmt"

	"github.com/golusoris/golusoris/pubsub/kafka"
)

func ExampleNewRecord() {
	r := kafka.NewRecord("orders", []byte("order-id"), []byte(`{"total":42}`))
	fmt.Println(r.Topic)
	fmt.Println(string(r.Key))
	fmt.Println(r.Timestamp.IsZero())
	// Output:
	// orders
	// order-id
	// true
}
