// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package dra

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/api/validate/content"
	"k8s.io/dynamic-resource-allocation/resourceslice"
)

// ErrInvalidDevice reports a device the resource.k8s.io/v1 API would reject.
var ErrInvalidDevice = errors.New("dra: invalid device")

// semverPattern is the semver.org 2.0.0 grammar VersionValue requires.
var semverPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
	`(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?` +
	`(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

// Device is one allocatable device a node exposes. Attribute and capacity
// names are C identifiers (driver domain) or `<dns-subdomain>/<identifier>`;
// names are unique across all maps, at most 32 per device.
type Device struct {
	// Name is a DNS label unique within the pool, e.g. "gpu-0".
	Name string
	// Strings holds string attributes, e.g. "backend": "cuda" (<= 64 bytes).
	Strings map[string]string
	// Ints holds integer attributes, e.g. "index": 0.
	Ints map[string]int64
	// Bools holds boolean attributes.
	Bools map[string]bool
	// Versions holds semver 2.0.0 attributes, e.g. "cudaVersion": "12.4.0".
	Versions map[string]string
	// Capacity holds quantities in base units, e.g. "memory": 24 << 30.
	Capacity map[string]int64
}

// toAPI validates d and converts it to the resource.k8s.io/v1 shape.
func (d Device) toAPI() (resourceapi.Device, error) {
	if errs := content.IsDNS1123Label(d.Name); len(errs) > 0 {
		return resourceapi.Device{}, fmt.Errorf("%w: name %q: %s", ErrInvalidDevice, d.Name, strings.Join(errs, "; "))
	}
	n := len(d.Strings) + len(d.Ints) + len(d.Bools) + len(d.Versions) + len(d.Capacity)
	if n > resourceapi.ResourceSliceMaxAttributesAndCapacitiesPerDevice {
		return resourceapi.Device{}, fmt.Errorf("%w: %s: %d attributes and capacities, limit %d",
			ErrInvalidDevice, d.Name, n, resourceapi.ResourceSliceMaxAttributesAndCapacitiesPerDevice)
	}
	attrs := make(map[resourceapi.QualifiedName]resourceapi.DeviceAttribute, n)
	err := errors.Join(
		addAttrs(attrs, d.Strings, stringAttr),
		addAttrs(attrs, d.Ints, func(v int64) (resourceapi.DeviceAttribute, error) {
			return resourceapi.DeviceAttribute{IntValue: &v}, nil
		}),
		addAttrs(attrs, d.Bools, func(v bool) (resourceapi.DeviceAttribute, error) {
			return resourceapi.DeviceAttribute{BoolValue: &v}, nil
		}),
		addAttrs(attrs, d.Versions, versionAttr),
	)
	if err != nil {
		return resourceapi.Device{}, fmt.Errorf("%w: %s: %w", ErrInvalidDevice, d.Name, err)
	}
	capacity, err := capacities(attrs, d.Capacity)
	if err != nil {
		return resourceapi.Device{}, fmt.Errorf("%w: %s: %w", ErrInvalidDevice, d.Name, err)
	}
	out := resourceapi.Device{Name: d.Name}
	if len(attrs) > 0 {
		out.Attributes = attrs
	}
	if len(capacity) > 0 {
		out.Capacity = capacity
	}
	return out, nil
}

func stringAttr(v string) (resourceapi.DeviceAttribute, error) {
	if len(v) > resourceapi.DeviceAttributeMaxValueLength {
		return resourceapi.DeviceAttribute{}, fmt.Errorf("string value longer than %d bytes", resourceapi.DeviceAttributeMaxValueLength)
	}
	return resourceapi.DeviceAttribute{StringValue: &v}, nil
}

func versionAttr(v string) (resourceapi.DeviceAttribute, error) {
	if len(v) > resourceapi.DeviceAttributeMaxValueLength || !semverPattern.MatchString(v) {
		return resourceapi.DeviceAttribute{}, fmt.Errorf("version %q is not semver 2.0.0 of at most %d bytes",
			v, resourceapi.DeviceAttributeMaxValueLength)
	}
	return resourceapi.DeviceAttribute{VersionValue: &v}, nil
}

func addAttrs[V any](
	dst map[resourceapi.QualifiedName]resourceapi.DeviceAttribute,
	src map[string]V,
	mk func(V) (resourceapi.DeviceAttribute, error),
) error {
	for _, name := range slices.Sorted(maps.Keys(src)) {
		if err := validateQualifiedName(name); err != nil {
			return err
		}
		qn := resourceapi.QualifiedName(name)
		if _, dup := dst[qn]; dup {
			return fmt.Errorf("attribute %q set twice", name)
		}
		attr, err := mk(src[name])
		if err != nil {
			return fmt.Errorf("attribute %q: %w", name, err)
		}
		dst[qn] = attr
	}
	return nil
}

func capacities(
	attrs map[resourceapi.QualifiedName]resourceapi.DeviceAttribute,
	src map[string]int64,
) (map[resourceapi.QualifiedName]resourceapi.DeviceCapacity, error) {
	out := make(map[resourceapi.QualifiedName]resourceapi.DeviceCapacity, len(src))
	for _, name := range slices.Sorted(maps.Keys(src)) {
		if err := validateQualifiedName(name); err != nil {
			return nil, err
		}
		qn := resourceapi.QualifiedName(name)
		if _, dup := attrs[qn]; dup {
			return nil, fmt.Errorf("capacity %q also set as attribute", name)
		}
		if src[name] < 0 {
			return nil, fmt.Errorf("capacity %q is negative", name)
		}
		out[qn] = resourceapi.DeviceCapacity{Value: *resource.NewQuantity(src[name], resource.BinarySI)}
	}
	return out, nil
}

// validateQualifiedName checks the QualifiedName grammar of resource.k8s.io/v1.
func validateQualifiedName(name string) error {
	domain, id, qualified := strings.Cut(name, "/")
	if !qualified {
		id = domain
	} else if len(domain) > resourceapi.DeviceMaxDomainLength || len(content.IsDNS1123Subdomain(domain)) > 0 {
		return fmt.Errorf("name %q: domain must be a DNS subdomain of at most %d bytes", name, resourceapi.DeviceMaxDomainLength)
	}
	if len(id) > resourceapi.DeviceMaxIDLength || len(content.IsCIdentifier(id)) > 0 {
		return fmt.Errorf("name %q: identifier must be a C identifier of at most %d bytes", name, resourceapi.DeviceMaxIDLength)
	}
	return nil
}

// buildPool converts devices into one pool split into slices of at most
// ResourceSliceMaxDevices; no devices still yields one empty slice so the
// pool shows the driver is up.
func buildPool(devices []Device) (resourceslice.Pool, error) {
	converted := make([]resourceapi.Device, 0, len(devices))
	seen := make(map[string]struct{}, len(devices))
	for _, d := range devices {
		if _, dup := seen[d.Name]; dup {
			return resourceslice.Pool{}, fmt.Errorf("%w: name %q used twice", ErrInvalidDevice, d.Name)
		}
		seen[d.Name] = struct{}{}
		api, err := d.toAPI()
		if err != nil {
			return resourceslice.Pool{}, err
		}
		converted = append(converted, api)
	}
	chunks := make([]resourceslice.Slice, 0, len(converted)/resourceapi.ResourceSliceMaxDevices+1)
	for chunk := range slices.Chunk(converted, resourceapi.ResourceSliceMaxDevices) {
		chunks = append(chunks, resourceslice.Slice{Devices: chunk})
	}
	if len(chunks) == 0 {
		chunks = append(chunks, resourceslice.Slice{Devices: []resourceapi.Device{}})
	}
	return resourceslice.Pool{Slices: chunks}, nil
}
