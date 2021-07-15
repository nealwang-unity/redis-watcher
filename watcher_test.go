package rediswatcher

import (
	"encoding/json"
	"fmt"
	"github.com/casbin/casbin/v2/model"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/casbin/casbin/v2"
)

func initWatcher(t *testing.T) (*casbin.Enforcer, *Watcher) {
	w, err := NewWatcher(WatcherOptions{
		Addresses: []string {"127.0.0.1:6379"},
		Namespace: "foo",
	})
	if err != nil {
		t.Fatalf("Failed to connect to Redis: %v", err)
	}

	e, err := casbin.NewEnforcer("examples/rbac_model.conf", "examples/rbac_policy.csv")
	if err != nil {
		t.Fatalf("Failed to create enforcer: %v", err)
	}
	_ = e.SetWatcher(w)
	return e, w.(*Watcher)
}
func TestWatcher(t *testing.T) {
	_, w := initWatcher(t)
	_ = w.SetUpdateCallback(func(s string) {
		fmt.Println(s)
	})
	_ = w.Update()
	w.Close()
	time.Sleep(time.Millisecond * 500)
}

func TestUpdate(t *testing.T) {
	_, w := initWatcher(t)
	_ = w.SetUpdateCallback(func(s string) {
		CustomDefaultFunc(
			func(id string, params interface{}) {
				t.Fatalf("method mapping error")
			},
		)(s, func(ID string, params interface{}) {
			if ID != w.options.LocalID {
				t.Fatalf("instance ID should be %s instead of %s", w.options.LocalID, ID)
			}
		}, nil, nil, nil, nil)
	})
	_ = w.Update()
	w.Close()
	time.Sleep(time.Millisecond * 500)
}
func TestUpdateForAddPolicy(t *testing.T) {
	e, w := initWatcher(t)
	_ = w.SetUpdateCallback(func(s string) {
		CustomDefaultFunc(
			func(id string, params interface{}) {
				t.Fatalf("method mapping error")
			},
		)(s, nil, func(ID string, params interface{}) {
			if ID != w.options.LocalID {
				t.Fatalf("instance ID should be %s instead of %s", w.options.LocalID, ID)
			}
			expected := fmt.Sprintf("%v", []string{"alice", "book1", "write"})
			res := fmt.Sprintf("%v", params)
			if expected != res {
				t.Fatalf("instance Params should be %s instead of %s", expected, res)
			}
		}, nil, nil, nil)
	})
	_, _ = e.AddPolicy("alice", "book1", "write")
	w.Close()
	time.Sleep(time.Millisecond * 500)
}
func TestUpdateForRemovePolicy(t *testing.T) {
	e, w := initWatcher(t)
	_ = w.SetUpdateCallback(func(s string) {
		CustomDefaultFunc(
			func(id string, params interface{}) {
				t.Fatalf("method mapping error")
			},
		)(s, nil, nil, func(ID string, params interface{}) {
			if ID != w.options.LocalID {
				t.Fatalf("instance ID should be %s instead of %s", w.options.LocalID, ID)
			}
			expected := fmt.Sprintf("%s", []string{"alice", "data1", "read"})
			res := fmt.Sprintf("%s", params)
			if expected != res {
				t.Fatalf("instance Params should be %s instead of %s", expected, res)
			}
		}, nil, nil)
	})
	_, _ = e.RemovePolicy("alice", "data1", "read")
	w.Close()
	time.Sleep(time.Millisecond * 500)
}

func TestUpdateForRemoveFilteredPolicy(t *testing.T) {
	e, w := initWatcher(t)
	_ = w.SetUpdateCallback(func(s string) {
		CustomDefaultFunc(
			func(id string, params interface{}) {
				t.Fatalf("method mapping error")
			},
		)(s, nil, nil, nil, func(ID string, params interface{}) {
			if ID != w.options.LocalID {
				t.Fatalf("instance ID should be %s instead of %s", w.options.LocalID, ID)
			}
			expected := fmt.Sprintf("%d %s", 1, strings.Join([]string{"data1", "read"}, " "))
			res := params.(string)
			if res != expected {
				t.Fatalf("instance Params should be %s instead of %s", expected, res)
			}
		}, nil)
	})
	_, _ = e.RemoveFilteredPolicy(1, "data1", "read")
	w.Close()
	time.Sleep(time.Millisecond * 500)
}

func TestUpdateSavePolicy(t *testing.T) {
	e, w := initWatcher(t)
	_ = w.SetUpdateCallback(func(s string) {
		CustomDefaultFunc(
			func(id string, params interface{}) {
				t.Fatalf("method mapping error")
			},
		)(s, nil, nil, nil, nil, func(ID string, params interface{}) {
			if ID != w.options.LocalID {
				t.Fatalf("instance ID should be %s instead of %s", w.options.LocalID, ID)
			}
			s := `{"e":{"e":{"Key":"e","Value":"some(where (p_eft == allow))","Tokens":null,"Policy":null,"PolicyMap":{},"RM":null}},"g":{"g":{"Key":"g","Value":"_, _","Tokens":null,"Policy":[["alice","data2_admin"]],"PolicyMap":{"alice,data2_admin":0},"RM":{}}},"logger":{"logger":{"Key":"","Value":"","Tokens":null,"Policy":null,"PolicyMap":null,"RM":null}},"m":{"m":{"Key":"m","Value":"g(r_sub, p_sub) \u0026\u0026 r_obj == p_obj \u0026\u0026 r_act == p_act","Tokens":null,"Policy":null,"PolicyMap":{},"RM":null}},"p":{"p":{"Key":"p","Value":"sub, obj, act","Tokens":["p_sub","p_obj","p_act"],"Policy":[["alice","data1","read"],["bob","data2","write"],["data2_admin","data2","read"],["data2_admin","data2","write"]],"PolicyMap":{"alice,data1,read":0,"bob,data2,write":1,"data2_admin,data2,read":2,"data2_admin,data2,write":3},"RM":null}},"r":{"r":{"Key":"r","Value":"sub, obj, act","Tokens":["r_sub","r_obj","r_act"],"Policy":null,"PolicyMap":{},"RM":null}}}`
			expected := model.Model{}
			_ = json.Unmarshal([]byte(s), &expected)
			bytes, _ := json.Marshal(params)
			res := model.Model{}
			_ = json.Unmarshal(bytes, &res)
			if !reflect.DeepEqual(res.GetPolicy("p", "p"), expected.GetPolicy("p", "p")) {
				t.Fatalf("instance Params should be %#v instead of %#v", expected, res)
			}
			if !reflect.DeepEqual(res.GetPolicy("g", "g"), expected.GetPolicy("g", "g")) {
				t.Fatalf("instance Params should be %#v instead of %#v", expected, res)
			}
		})
	})
	_ = e.SavePolicy()
	w.Close()
	time.Sleep(time.Millisecond * 500)
}
