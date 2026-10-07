// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package dra_test

import (
	"fmt"
	"strconv"
	"sync"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// apiServerLike adds the API server behaviour the ResourceSlice controller
// relies on and the fake clientset lacks: GenerateName, integer
// ResourceVersions and spec field selectors on list and watch. The create
// and update reactors mirror the upstream controller tests.
func apiServerLike(k *fake.Clientset) {
	var mu sync.Mutex
	counter := 0
	k.PrependReactor("create", "resourceslices", func(action k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		defer mu.Unlock()
		s, ok := action.(k8stesting.CreateAction).GetObject().(*resourceapi.ResourceSlice)
		if !ok {
			return false, nil, nil
		}
		s.ResourceVersion = "1"
		if s.Name == "" && s.GenerateName != "" {
			s.Name = fmt.Sprintf("%s%d", s.GenerateName, counter)
		}
		counter++
		return false, nil, nil
	})
	k.PrependReactor("update", "resourceslices", func(action k8stesting.Action) (bool, runtime.Object, error) {
		s, ok := action.(k8stesting.UpdateAction).GetObject().(*resourceapi.ResourceSlice)
		if !ok {
			return false, nil, nil
		}
		rev, err := strconv.Atoi(s.ResourceVersion)
		if err != nil {
			rev = 0
		}
		s.ResourceVersion = strconv.Itoa(rev + 1)
		return false, nil, nil
	})
	gvr := resourceapi.SchemeGroupVersion.WithResource("resourceslices")
	gvk := resourceapi.SchemeGroupVersion.WithKind("ResourceSlice")
	k.PrependReactor("list", "resourceslices", func(action k8stesting.Action) (bool, runtime.Object, error) {
		sel := action.(k8stesting.ListAction).GetListRestrictions().Fields
		obj, err := k.Tracker().List(gvr, gvk, "")
		if err != nil {
			return true, nil, err
		}
		list, ok := obj.(*resourceapi.ResourceSliceList)
		if !ok {
			return true, nil, fmt.Errorf("unexpected list type %T", obj)
		}
		kept := list.Items[:0]
		for _, s := range list.Items {
			if matches(sel, &s) {
				kept = append(kept, s)
			}
		}
		list.Items = kept
		return true, list, nil
	})
	k.PrependWatchReactor("resourceslices", func(action k8stesting.Action) (bool, watch.Interface, error) {
		sel := action.(k8stesting.WatchAction).GetWatchRestrictions().Fields
		w, err := k.Tracker().Watch(gvr, "")
		if err != nil {
			return true, nil, err
		}
		return true, watch.Filter(w, func(e watch.Event) (watch.Event, bool) {
			s, ok := e.Object.(*resourceapi.ResourceSlice)
			return e, !ok || matches(sel, s)
		}), nil
	})
}

func matches(sel fields.Selector, s *resourceapi.ResourceSlice) bool {
	if sel == nil || sel.Empty() {
		return true
	}
	nodeName := ""
	if s.Spec.NodeName != nil {
		nodeName = *s.Spec.NodeName
	}
	return sel.Matches(fields.Set{
		resourceapi.ResourceSliceSelectorDriver:   s.Spec.Driver,
		resourceapi.ResourceSliceSelectorNodeName: nodeName,
		resourceapi.ResourceSliceSelectorPoolName: s.Spec.Pool.Name,
	})
}
