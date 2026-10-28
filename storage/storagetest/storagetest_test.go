// SPDX-License-Identifier: Apache-2.0

package storagetest

import (
	"reflect"
	"testing"

	"go.toshicloud.io/backend/storage"
)

// TestEveryObjectStoreMethodHasACase fails when ObjectStore gains a method
// no case exercises, or a case names a method it does not have.
func TestEveryObjectStoreMethodHasACase(t *testing.T) {
	iface := reflect.TypeFor[storage.ObjectStore]()
	covered := map[string]bool{}
	for _, c := range cases {
		for _, m := range c.methods {
			if _, ok := iface.MethodByName(m); !ok {
				t.Errorf("case %s names %s, which ObjectStore does not have", c.name, m)
			}
			covered[m] = true
		}
	}
	for i := range iface.NumMethod() {
		if name := iface.Method(i).Name; !covered[name] {
			t.Errorf("no case exercises ObjectStore.%s", name)
		}
	}
}
