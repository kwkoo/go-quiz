package pkg

import (
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

func InitRedis(redisHost, redisPassword string, shutdownArtifacts *ShutdownArtifacts) *PersistenceEngine {
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

	shutdownArtifacts.Wg.Add(1)
	go func() {
		<-shutdownArtifacts.Ch
		pool.Close()
		log.Print("persistence engine graceful shutdown")
		shutdownArtifacts.Wg.Done()
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

func (engine *PersistenceEngine) Set(key string, value []byte, expiry int) error {
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
	conn := engine.pool.Get()
	defer conn.Close()

	conn.Do("DEL", key)
}

func (engine *PersistenceEngine) Incr(counterKey string) (int, error) {

	conn := engine.pool.Get()
	defer conn.Close()

	return redis.Int(conn.Do("INCR", counterKey))
}
