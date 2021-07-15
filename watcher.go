package rediswatcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/casbin/casbin/v2/model"

	"github.com/casbin/casbin/v2/persist"
	rds "github.com/go-redis/redis/v7"
)

type RedisClient interface {
	Ping() *rds.StatusCmd
	Get(key string) *rds.StringCmd
	Set(key string, value interface{}, expiration time.Duration) *rds.StatusCmd
	Watch(handler func(*rds.Tx) error, keys ...string) error
	Del(keys ...string) *rds.IntCmd
	SetNX(key string, value interface{}, expiration time.Duration) *rds.BoolCmd
	Eval(script string, keys []string, args ...interface{}) *rds.Cmd
	Scan(cursor uint64, match string, count int64) *rds.ScanCmd
	LPush(key string, values ...interface{}) *rds.IntCmd
	Publish(channel string, message interface{}) *rds.IntCmd
	Subscribe(channels ...string) *rds.PubSub
	Close() error
}

type Watcher struct {
	l         sync.Mutex
	subClient RedisClient
	pubClient RedisClient
	options   WatcherOptions
	close     chan struct{}
	callback  func(string)
	ctx       context.Context
}

type MSG struct {
	Method string
	ID     string
	Sec    string
	Ptype  string
	Params interface{}
}

func (m *MSG) MarshalBinary() ([]byte, error) {
	return json.Marshal(m)
}

// UnmarshalBinary decodes the struct into a User
func (m *MSG) UnmarshalBinary(data []byte) error {
	if err := json.Unmarshal(data, m); err != nil {
		return err
	}
	return nil
}

// NewWatcher creates a new Watcher to be used with a Casbin enforcer
// addr is a redis target string in the format "host:port"
// setters allows for inline WatcherOptions
//
// 		Example:
// 				w, err := rediswatcher.NewWatcher("127.0.0.1:6379",WatcherOptions{}, nil)
//
func NewWatcher(option WatcherOptions) (persist.Watcher, error) {
	if len(option.Addresses) == 0 || option.Addresses[0] == "" {
		return nil, errors.New("redis: missing redis node address(es)")
	}
	if option.Namespace == "" {
		return nil, errors.New("redis: missing key namespace")
	}
	if option.UseSentinel && option.MasterName == "" {
		return nil, errors.New("redis: missing MasterName for Sentinel setup")
	}

	if option.MaxConnections == 0 {
		// This is the exact same logic the redis client uses under the hood, but I wanted to
		// make our copy of the connection count match theirs.
		option.MaxConnections = uint(10 * runtime.NumCPU())
	}

	initConfig(&option)

	var w *Watcher

	if option.UseSentinel {
		if option.MasterName == "" {
			return nil, errors.New("redis: missing MasterName for Sentinel setup")
		}

		w = &Watcher{
			subClient: rds.NewFailoverClient(&rds.FailoverOptions{
				MasterName:    option.MasterName,
				SentinelAddrs: option.Addresses,
				PoolSize:      int(option.MaxConnections),
			}),
			pubClient: rds.NewFailoverClient(&rds.FailoverOptions{
				MasterName:    option.MasterName,
				SentinelAddrs: option.Addresses,
				PoolSize:      int(option.MaxConnections),
			}),
			ctx:       context.Background(),
			close:     make(chan struct{}),
		}
	} else if len(option.Addresses) > 1 {
		w = &Watcher{
			subClient: rds.NewClusterClient(&rds.ClusterOptions{
				Addrs: option.Addresses,
				Password: option.Password,
				PoolSize: int(option.MaxConnections),
			}),
			pubClient: rds.NewClusterClient(&rds.ClusterOptions{
				Addrs: option.Addresses,
				Password: option.Password,
				PoolSize: int(option.MaxConnections),
			}),
			ctx:       context.Background(),
			close:     make(chan struct{}),
		}
	} else {
		w = &Watcher{
			subClient: rds.NewClient(&rds.Options{
				Addr: option.Addresses[0],
				Password: option.Password,
			}),
			pubClient: rds.NewClient(&rds.Options{
				Addr: option.Addresses[0],
				Password: option.Password,
			}),
			ctx:       context.Background(),
			close:     make(chan struct{}),
		}
	}



	w.initConfig(option)

	if err := w.subClient.Ping().Err(); err != nil {
		return nil, err
	}
	if err := w.pubClient.Ping().Err(); err != nil {
		return nil, err
	}

	w.options = option

	w.subscribe()

	return w, nil
}

func (w *Watcher) initConfig(option WatcherOptions) error {
	var err error
	if option.OptionalUpdateCallback != nil {
		err = w.SetUpdateCallback(option.OptionalUpdateCallback)
	} else {
		err = w.SetUpdateCallback(func(string) {
			log.Println("Casbin Redis Watcher callback not set when an update was received")
		})
	}
	if err != nil {
		return err
	}

	if option.SubClient != nil {
		w.subClient = option.SubClient
	}

	if option.PubClient != nil {
		w.pubClient = option.PubClient
	}

	return nil
}

// NewPublishWatcher return a Watcher only publish but not subscribe
func NewPublishWatcher(addr string, option WatcherOptions) (persist.Watcher, error) {
	option.Addr = addr
	w := &Watcher{
		pubClient: rds.NewClient(&option.Options),
		ctx:       context.Background(),
		close:     make(chan struct{}),
	}

	initConfig(&option)
	w.options = option

	return w, nil
}

// SetUpdateCallback SetUpdateCallBack sets the update callback function invoked by the watcher
// when the policy is updated. Defaults to Enforcer.LoadPolicy()
func (w *Watcher) SetUpdateCallback(callback func(string)) error {
	w.l.Lock()
	w.callback = callback
	w.l.Unlock()
	return nil
}

// Update publishes a message to all other casbin instances telling them to
// invoke their update callback
func (w *Watcher) Update() error {
	return w.logRecord(func() error {
		w.l.Lock()
		defer w.l.Unlock()
		return w.pubClient.Publish(w.options.Channel, &MSG{"Update", w.options.LocalID, "", "", ""}).Err()
	})
}

// UpdateForAddPolicy calls the update callback of other instances to synchronize their policy.
// It is called after Enforcer.AddPolicy()
func (w *Watcher) UpdateForAddPolicy(sec, ptype string, params ...string) error {
	return w.logRecord(func() error {
		w.l.Lock()
		defer w.l.Unlock()
		return w.pubClient.Publish(w.options.Channel, &MSG{"UpdateForAddPolicy", w.options.LocalID, sec, ptype, params}).Err()
	})
}

// UpdateForRemovePolicy UPdateForRemovePolicy calls the update callback of other instances to synchronize their policy.
// It is called after Enforcer.RemovePolicy()
func (w *Watcher) UpdateForRemovePolicy(sec, ptype string, params ...string) error {
	return w.logRecord(func() error {
		w.l.Lock()
		defer w.l.Unlock()
		return w.pubClient.Publish(w.options.Channel, &MSG{"UpdateForRemovePolicy", w.options.LocalID, sec, ptype, params}).Err()
	})
}

// UpdateForRemoveFilteredPolicy calls the update callback of other instances to synchronize their policy.
// It is called after Enforcer.RemoveFilteredNamedGroupingPolicy()
func (w *Watcher) UpdateForRemoveFilteredPolicy(sec, ptype string, fieldIndex int, fieldValues ...string) error {
	return w.logRecord(func() error {
		w.l.Lock()
		defer w.l.Unlock()
		return w.pubClient.Publish(w.options.Channel,
			&MSG{"UpdateForRemoveFilteredPolicy", w.options.LocalID,
				sec,
				ptype,
				fmt.Sprintf("%d %s", fieldIndex, strings.Join(fieldValues, " ")),
			},
		).Err()
	})
}

// UpdateForSavePolicy calls the update callback of other instances to synchronize their policy.
// It is called after Enforcer.RemoveFilteredNamedGroupingPolicy()
func (w *Watcher) UpdateForSavePolicy(model model.Model) error {
	return w.logRecord(func() error {
		w.l.Lock()
		defer w.l.Unlock()
		return w.pubClient.Publish(w.options.Channel, &MSG{"UpdateForSavePolicy", w.options.LocalID, "", "", model}).Err()
	})
}

func (w *Watcher) logRecord(f func() error) error {
	err := f()
	if err != nil {
		log.Println(err)
	}
	return err
}

func (w *Watcher) unsubscribe(psc *rds.PubSub) error {
	return psc.Unsubscribe()
}

func (w *Watcher) subscribe() {
	w.l.Lock()
	sub := w.subClient.Subscribe(w.options.Channel)
	w.l.Unlock()
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer func() {
			err := sub.Close()
			if err != nil {
				log.Println(err)
			}
			err = w.pubClient.Close()
			if err != nil {
				log.Println(err)
			}
			err = w.subClient.Close()
			if err != nil {
				log.Println(err)
			}
		}()
		ch := sub.Channel()
		wg.Done()
		for msg := range ch {
			select {
			case <-w.close:
				return
			default:
			}
			data := msg.Payload
			w.callback(data)
		}
	}()
	wg.Wait()
}

func (w *Watcher) GetWatcherOptions() WatcherOptions {
	w.l.Lock()
	defer w.l.Unlock()
	return w.options
}

func (w *Watcher) Close() {
	w.l.Lock()
	defer w.l.Unlock()
	close(w.close)
	w.pubClient.Publish(w.options.Channel, "Close")
}
