package cache

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	"github.com/rogpeppe/go-internal/cache"
	"sync"
	"time"
)

var (
	ErrCodeSendTooMany        = errors.New("发送验证码太频繁")
	ErrCodeVerifyTooManyTimes = errors.New("验证次数太多")
	ErrUnknownForCode         = errors.New("我也不知发生什么了，反正是跟 code 有关")
)

// 编译器会在编译的时候，把 set_code 的代码放进来这个 luaSetCode 变量里
//
//go:embed lua/set_code.lua
var luaSetCode string

//go:embed lua/verify_code.lua
var luaVerifyCode string

type CodeCache interface {
	Set(ctx context.Context, biz, phone, code string) error
	Verify(ctx context.Context, biz, phone, inputCode string) (bool, error)
}

type RedisCodeCache struct {
	client redis.Cmdable
}

// NewCodeCacheGoBestPractice Go 的最佳实践是返回具体类型
func NewCodeCacheGoBestPractice(client redis.Cmdable) *RedisCodeCache {
	return &RedisCodeCache{
		client: client,
	}
}

/*func NewCodeCache(client redis.Cmdable) CodeCache {
	return &RedisCodeCache{
		client: client,
	}
}*/

func (c *RedisCodeCache) Set(ctx context.Context, biz, phone, code string) error {
	res, err := c.client.Eval(ctx, luaSetCode, []string{c.key(biz, phone)}, code).Int()
	if err != nil {
		return err
	}
	switch res {
	case 0:
		// 毫无问题
		return nil
	case -1:
		// 发送太频繁
		return ErrCodeSendTooMany
	//case -2:
	//	return
	default:
		// 系统错误
		return errors.New("系统错误")
	}
}

func (c *RedisCodeCache) Verify(ctx context.Context, biz, phone, inputCode string) (bool, error) {
	res, err := c.client.Eval(ctx, luaVerifyCode, []string{c.key(biz, phone)}, inputCode).Int()
	if err != nil {
		return false, err
	}
	switch res {
	case 0:
		return true, nil
	case -1:
		// 正常来说，如果频繁出现这个错误，你就要告警，因为有人搞你
		return false, ErrCodeVerifyTooManyTimes
	case -2:
		return false, nil
		//default:
		//	return false, ErrUnknownForCode
	}
	return false, ErrUnknownForCode
}

//func (c *RedisCodeCache) Verify(ctx context.Context, biz, phone, code string) error {
//
//}

func (c *RedisCodeCache) key(biz, phone string) string {
	return fmt.Sprintf("phone_code:%s:%s", biz, phone)
}

// LocalCodeCache 假如说你要切换这个，你是不是得把 lua 脚本的逻辑，在这里再写一遍？
type LocalCodeCache struct {
	cache *cache.Cache
}

type localCodeCacheValue struct {
	code       string
	times      int64
	createTime int64
}

func NewCodeCache() CodeCache {
	return &LocalCodeCache{
		cache: cache.New(cache.NoExpiration, time.Minute*10),
	}
}

func (c *LocalCodeCache) getValue(code string) *localCodeCacheValue {
	return &localCodeCacheValue{
		code:       code,
		times:      3,
		createTime: time.Now().Unix(),
	}
}

func (c *LocalCodeCache) key(biz, phone string) string {
	return fmt.Sprintf("phone_code:%s:%s", biz, phone)
}

func (c *LocalCodeCache) Set(ctx context.Context, biz, phone, code string) error {
	var m sync.Mutex
	m.Lock()
	defer m.Unlock()

	//查找
	key := c.key(biz, phone)

	if item, found := c.cache.Get(key); found {
		//key存在,验证过期时间
		value, ok := item.(*localCodeCacheValue)
		if !ok {
			return ErrUnknownForCode
		}
		//小于1分钟
		if time.Now().Unix()-value.createTime < 60 {
			return ErrCodeSendTooMany
		}
	}

	c.cache.Set(key, c.getValue(code), time.Minute*5)
	return nil
}

func (c *LocalCodeCache) Verify(ctx context.Context, biz, phone, inputCode string) (bool, error) {

	var m sync.Mutex
	m.Lock()
	defer m.Unlock()

	//查找
	key := c.key(biz, phone)

	item, found := c.cache.Get(key)

	//没有
	if !found {
		return false, ErrUnknownForCode
	}

	value, ok := item.(*localCodeCacheValue)

	if !ok {
		return false, ErrUnknownForCode
	}

	//说明，用户一直输错，有人搞你
	//或者已经用过了，也是有人搞你
	if value.times <= 0 {
		return false, ErrCodeVerifyTooManyTimes
	}

	//用户手一抖，输错了
	//可验证次数 -1
	if value.code != inputCode {
		value.times--
		c.cache.Set(key, value, time.Minute*5)
		return false, ErrUnknownForCode
	}

	value.times = -1
	c.cache.Set(key, value, time.Second)
	return true, nil
}
