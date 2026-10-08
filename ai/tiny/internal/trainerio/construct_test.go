// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package trainerio

import (
	"errors"
	"testing"
)

type constructorFixture struct {
	value int
}

func TestConstructTrainerNormalizesBeforeBuild(t *testing.T) {
	t.Parallel()
	trainer, err := ConstructTrainer(
		constructorFixture{value: 1},
		func(options constructorFixture) (constructorFixture, error) {
			options.value++
			return options, nil
		},
		func(options constructorFixture) *constructorFixture { return &options },
	)
	if err != nil {
		t.Fatalf("ConstructTrainer() error = %v", err)
	}
	if trainer.value != 2 {
		t.Fatalf("ConstructTrainer() value = %d; want 2", trainer.value)
	}
}

func TestConstructTrainerStopsOnInvalidConstruction(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("invalid options")
	built := false
	trainer, err := ConstructTrainer(
		constructorFixture{},
		func(constructorFixture) (constructorFixture, error) {
			return constructorFixture{}, wantErr
		},
		func(options constructorFixture) *constructorFixture {
			built = true
			return &options
		},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("ConstructTrainer() error = %v; want %v", err, wantErr)
	}
	if trainer != nil || built {
		t.Fatal("ConstructTrainer() built a trainer after normalization failed")
	}
}

func TestConstructTrainerRejectsMissingOrNilBuilder(t *testing.T) {
	t.Parallel()
	normalize := func(options constructorFixture) (constructorFixture, error) {
		return options, nil
	}
	if trainer, err := ConstructTrainer[constructorFixture, constructorFixture](
		constructorFixture{}, nil, nil,
	); err == nil || trainer != nil {
		t.Fatal("ConstructTrainer() accepted nil callbacks")
	}
	if trainer, err := ConstructTrainer(
		constructorFixture{}, normalize, func(constructorFixture) *constructorFixture { return nil },
	); err == nil || trainer != nil {
		t.Fatal("ConstructTrainer() accepted a nil trainer")
	}
}
