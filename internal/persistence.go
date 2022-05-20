package internal

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/gomodule/redigo/redis"
)

type PersistenceEngine struct {
	pool *redis.Pool
}

// Redis helper functions
// Copied from https://github.com/pete911/examples-redigo

func InitRedis(redisHost, redisPassword string) *PersistenceEngine {
	// init redis connection pool
	// copied from https://github.com/pete911/examples-redigo
	pool := redis.Pool{
		MaxIdle:     3,
		IdleTimeout: 240 * time.Second,

		Dial: func() (redis.Conn, error) {
			var (
				c   redis.Conn
				err error
			)
			if redisPassword == "" {
				c, err = redis.Dial("tcp", redisHost)
			} else {
				c, err = redis.Dial("tcp", redisHost, redis.DialPassword(redisPassword))
			}
			if err != nil {
				return nil, err
			}
			return c, err
		},

		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			_, err := c.Do("PING")
			return err
		},
	}

	return &PersistenceEngine{pool: &pool}
}

// wait for Redis to come up
func (engine *PersistenceEngine) WaitForRedis() {
	if engine == nil {
		return
	}

	for {
		conn := engine.pool.Get()
		if conn.Err() == nil {
			conn.Close()
			return
		}
		log.Print("could not get connection to Redis, sleeping...")
		time.Sleep(5 * time.Second)
	}
}

func (engine *PersistenceEngine) Close() {
	if engine == nil {
		return
	}
	engine.pool.Close()
	log.Print("persistence engine shutdown")
}

func (engine *PersistenceEngine) GetKeys(prefix string) ([]string, error) {
	if engine == nil {
		return []string{}, nil
	}
	conn := engine.pool.Get()
	defer conn.Close()

	iter := 0
	keys := []string{}
	pattern := prefix + ":*"
	for {
		arr, err := redis.Values(conn.Do("SCAN", iter, "MATCH", pattern))
		if err != nil {
			return keys, fmt.Errorf("error retrieving %s keys: %v", pattern, err)
		}

		iter, _ = redis.Int(arr[0], nil)
		k, _ := redis.Strings(arr[1], nil)
		keys = append(keys, k...)
		if iter == 0 {
			break
		}
	}

	return keys, nil
}

func (engine *PersistenceEngine) Get(key string) ([]byte, error) {
	if engine == nil {
		return nil, nil
	}
	conn := engine.pool.Get()
	defer conn.Close()

	data, err := redis.Bytes(conn.Do("GET", key))
	if err != nil {
		return nil, fmt.Errorf("error getting value for key %s: %v", key, err)
	}
	return data, nil
}

func (engine *PersistenceEngine) Set(key string, value []byte, expiry int) error {
	if engine == nil {
		return nil
	}
	conn := engine.pool.Get()
	defer conn.Close()

	var err error
	if expiry == 0 {
		_, err = conn.Do("SET", key, value)
	} else {
		_, err = conn.Do("SET", key, value, "EX", expiry)
	}
	if err != nil {
		return fmt.Errorf("error setting key %s in redis: %v", key, err)
	}
	return nil
}

func (engine *PersistenceEngine) Delete(key string) {
	if engine == nil {
		return
	}
	conn := engine.pool.Get()
	defer conn.Close()

	conn.Do("DEL", key)
}

func (engine *PersistenceEngine) Incr(counterKey string) (int, error) {
	if engine == nil {
		return 0, errors.New("redis not configured")
	}
	conn := engine.pool.Get()
	defer conn.Close()

	return redis.Int(conn.Do("INCR", counterKey))
}
