package pkg

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
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

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Notify(c, syscall.SIGTERM)
	go func() {
		<-c
		pool.Close()
		log.Print("shutdown redis connection pool")
		os.Exit(0)
	}()

	return &PersistenceEngine{pool: &pool}
}

func (engine *PersistenceEngine) GetKeys(prefix string) ([]string, error) {
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
	conn := engine.pool.Get()
	defer conn.Close()

	data, err := redis.Bytes(conn.Do("GET", key))
	if err != nil {
		return nil, fmt.Errorf("error getting value for key %s: %v", key, err)
	}
	return data, nil
}

func (engine *PersistenceEngine) Set(key string, value []byte) error {
	conn := engine.pool.Get()
	defer conn.Close()

	_, err := conn.Do("SET", key, value)
	if err != nil {
		return fmt.Errorf("error setting key %s in redis: %v", key, err)
	}
	return nil
}
