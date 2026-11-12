package config

import (
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
)

// Store holds the live config. Readers call Get on every use so that a reload
// takes effect without restarting. A stored *AppConfig must never be mutated;
// a reload stores a new one instead, which keeps concurrent reads race-free.
type Store struct {
	p atomic.Pointer[AppConfig]
}

// NewStore returns a Store holding cfg.
func NewStore(cfg *AppConfig) *Store {
	s := &Store{}
	s.p.Store(cfg)
	return s
}

// Get returns the current config. Callers must treat it as read-only.
func (s *Store) Get() *AppConfig {
	return s.p.Load()
}

// Set replaces the current config.
func (s *Store) Set(cfg *AppConfig) {
	s.p.Store(cfg)
}

// Diff lists the settings that differ between old and new, one entry per
// setting, formatted as "key: old → new". Keys use their TOML names.
func Diff(old, new *AppConfig) []string {
	var out []string
	diffStruct(reflect.ValueOf(*old), reflect.ValueOf(*new), "", &out)
	return out
}

func diffStruct(a, b reflect.Value, prefix string, out *[]string) {
	t := a.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("toml"), ",")[0]
		if name == "" {
			name = f.Name
		}
		key := prefix + name
		av, bv := a.Field(i), b.Field(i)
		if f.Type.Kind() == reflect.Struct {
			diffStruct(av, bv, key+".", out)
			continue
		}
		if !reflect.DeepEqual(av.Interface(), bv.Interface()) {
			*out = append(*out, fmt.Sprintf("%s: %s → %s", key, show(av), show(bv)))
		}
	}
}

func show(v reflect.Value) string {
	if v.Kind() == reflect.String {
		return fmt.Sprintf("%q", v.String())
	}
	return fmt.Sprintf("%v", v.Interface())
}
