package gear

import (
	"bytes"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"time"

	"github.com/stretchr/testify/assert"
)

func CtxTest(app *App, method, url string, body io.Reader) *Context {
	req := httptest.NewRequest(method, url, body)
	res := httptest.NewRecorder()
	return NewContext(app, res, req)
}

func CtxResult(ctx *Context) *http.Response {
	res := ctx.Res.res.(*httptest.ResponseRecorder)
	return res.Result()
}

func CtxBody(ctx *Context) (val string) {
	body, err := ioutil.ReadAll(CtxResult(ctx).Body)
	if err == nil {
		val = bytes.NewBuffer(body).String()
	}
	return
}

func TestGearContextContextInterface(t *testing.T) {
	assert := assert.New(t)

	done := false
	app := New()
	app.Use(func(ctx *Context) error {
		// ctx.Deadline
		_, ok := ctx.Deadline()
		assert.False(ok)
		// ctx.Err
		assert.Nil(ctx.Err())
		// ctx.Value
		s := ctx.Value(http.ServerContextKey)
		EqualPtr(t, s, app.Server)

		go func() {
			// ctx.Done
			<-ctx.Done()
			done = true
		}()

		return ctx.End(204)
	})
	srv := app.Start()
	defer srv.Close()

	req := NewRequst()
	res, err := req.Get("http://" + srv.Addr().String())
	assert.Nil(err)
	assert.Equal(204, res.StatusCode)
	assert.True(done)
}

func TestGearContextWithContext(t *testing.T) {
	assert := assert.New(t)

	cancelDone := false
	deadlineDone := false
	timeoutDone := false

	app := New()
	app.Use(func(ctx *Context) error {
		// ctx.WithValue
		c := ctx.WithValue("test", "abc")
		assert.Equal("abc", c.Value("test").(string))
		s := c.Value(http.ServerContextKey)
		EqualPtr(t, s, app.Server)

		c1, _ := ctx.WithCancel()
		c2, _ := ctx.WithDeadline(time.Now().Add(time.Second))
		c3, _ := ctx.WithTimeout(time.Second)

		go func() {
			<-c1.Done()
			assert.True(ctx.ended)
			assert.Nil(ctx.afterHooks)
			cancelDone = true
		}()

		go func() {
			<-c2.Done()
			assert.True(ctx.ended)
			assert.Nil(ctx.afterHooks)
			deadlineDone = true
		}()

		go func() {
			<-c3.Done()
			assert.True(ctx.ended)
			assert.Nil(ctx.afterHooks)
			timeoutDone = true
		}()

		ctx.Status(404)
		ctx.Cancel()
		assert.True(ctx.ended)
		assert.Nil(ctx.afterHooks)

		return nil
	})
	app.Use(func(ctx *Context) error {
		panic("this middleware unreachable")
	})

	srv := app.Start()
	defer srv.Close()

	req := NewRequst()
	res, err := req.Get("http://" + srv.Addr().String())
	assert.Nil(err)
	assert.Equal(404, res.StatusCode)
	assert.True(cancelDone)
	assert.True(deadlineDone)
	assert.True(timeoutDone)
}

// ----- Test Context.Any -----
type ctxAnyType struct{}
type ctxAnyResult struct {
	Host string
	Path string
}

var ctxAny = &ctxAnyType{}

func (t *ctxAnyType) New(ctx *Context) (interface{}, error) {
	if ctx.Method != "GET" {
		return nil, errors.New(ctx.Method)
	}
	return &ctxAnyResult{Host: ctx.Host, Path: ctx.Path}, nil
}

func TestGearContextAny(t *testing.T) {
	app := New()

	t.Run("type Any", func(t *testing.T) {
		t.Run("should get the same value with the same ctx", func(t *testing.T) {
			assert := assert.New(t)

			ctx := CtxTest(app, "GET", "http://example.com/foo", nil)
			val, err := ctx.Any(ctxAny)
			assert.Nil(err)
			res := val.(*ctxAnyResult)
			assert.Equal(ctx.Host, res.Host)
			assert.Equal(ctx.Path, res.Path)

			val2, _ := ctx.Any(ctxAny)
			EqualPtr(t, val, val2)
		})

		t.Run("should get different value with different ctx", func(t *testing.T) {
			assert := assert.New(t)

			ctx := CtxTest(app, "GET", "http://example.com/foo", nil)
			val, err := ctx.Any(ctxAny)
			assert.Nil(err)

			ctx2 := CtxTest(app, "GET", "http://example.com/foo", nil)
			val2, err2 := ctx2.Any(ctxAny)
			assert.Nil(err2)
			NotEqualPtr(t, val, val2)
		})

		t.Run("should get error", func(t *testing.T) {
			assert := assert.New(t)

			ctx := CtxTest(app, "POST", "http://example.com/foo", nil)
			val, err := ctx.Any(ctxAny)
			assert.Nil(val)
			assert.NotNil(err)
			assert.Equal("POST", err.Error())
		})
	})

	t.Run("SetAny with interface{}", func(t *testing.T) {
		assert := assert.New(t)

		ctx := CtxTest(app, "POST", "http://example.com/foo", nil)
		val, err := ctx.Any(struct{}{})
		assert.Nil(val)
		assert.Equal("[App] non-existent key", err.Error())

		ctx.SetAny(struct{}{}, true)
		val, err = ctx.Any(struct{}{})
		assert.Nil(err)
		assert.True(val.(bool))
	})

	t.Run("Setting", func(t *testing.T) {
		assert := assert.New(t)

		ctx := CtxTest(app, "POST", "http://example.com/foo", nil)
		assert.Equal("development", ctx.Setting("AppEnv").(string))
	})
}

func TestGearContextSetting(t *testing.T) {
	assert := assert.New(t)
	val := map[string]int{"abc": 123}

	app := New()
	app.Set("someKey", val)
	ctx := CtxTest(app, "POST", "http://example.com/foo", nil)

	assert.Nil(ctx.Setting("key"))
	assert.Equal(val, ctx.Setting("someKey").(map[string]int))
}

func TestGearContextIP(t *testing.T) {
	assert := assert.New(t)

	app := New()
	r := NewRouter()
	r.Get("/XForwardedFor", func(ctx *Context) error {
		assert.Equal("127.0.0.10", ctx.IP().String())
		return ctx.End(http.StatusNoContent)
	})
	r.Get("/XRealIP", func(ctx *Context) error {
		assert.Equal("127.0.0.20", ctx.IP().String())
		return ctx.End(http.StatusNoContent)
	})
	r.Get("/", func(ctx *Context) error {
		assert.NotNil(ctx.IP())
		return ctx.End(http.StatusNoContent)
	})
	r.Get("/err", func(ctx *Context) error {
		assert.Nil(ctx.IP())
		return ctx.End(http.StatusNoContent)
	})
	app.UseHandler(r)

	srv := app.Start()
	defer srv.Close()

	host := "http://" + srv.Addr().String()
	req := NewRequst()
	req.Headers["X-Forwarded-For"] = "127.0.0.10"
	res, err := req.Get(host + "/XForwardedFor")
	assert.Nil(err)
	assert.Equal(204, res.StatusCode)

	req = NewRequst()
	req.Headers["X-Real-IP"] = "127.0.0.20"
	res, err = req.Get(host + "/XRealIP")
	assert.Nil(err)
	assert.Equal(204, res.StatusCode)

	req = NewRequst()
	res, err = req.Get(host)
	assert.Nil(err)
	assert.Equal(204, res.StatusCode)

	req = NewRequst()
	req.Headers["X-Real-IP"] = "1.2.3"
	res, err = req.Get(host + "/err")
	assert.Nil(err)
	assert.Equal(204, res.StatusCode)
}

func TestGearContextParam(t *testing.T) {
	assert := assert.New(t)

	app := New()
	r := NewRouter()
	r.Get("/api/:type/:id", func(ctx *Context) error {
		assert.Equal("user", ctx.Param("type"))
		assert.Equal("123", ctx.Param("id"))
		assert.Equal("", ctx.Param("other"))
		return ctx.End(http.StatusNoContent)
	})
	r.Get("/view/:all*", func(ctx *Context) error {
		assert.Equal("user/123", ctx.Param("all"))
		return ctx.End(http.StatusNoContent)
	})
	app.UseHandler(r)

	srv := app.Start()
	defer srv.Close()

	host := "http://" + srv.Addr().String()
	req := NewRequst()
	res, err := req.Get(host + "/api/user/123")
	assert.Nil(err)
	assert.Equal(204, res.StatusCode)

	req = NewRequst()
	res, err = req.Get(host + "/view/user/123")
	assert.Nil(err)
	assert.Equal(204, res.StatusCode)
}

func TestGearContextQuery(t *testing.T) {
	assert := assert.New(t)

	app := New()
	r := NewRouter()
	r.Get("/api", func(ctx *Context) error {
		assert.Equal("user", ctx.Query("type"))
		assert.Equal("123", ctx.Query("id"))
		assert.Equal([]string{"123"}, ctx.QueryValues("id"))
		assert.Equal("", ctx.Query("other"))
		return ctx.End(http.StatusNoContent)
	})
	r.Get("/view", func(ctx *Context) error {
		assert.Equal("123", ctx.Query("id"))
		assert.Equal([]string{"123", "abc"}, ctx.QueryValues("id"))
		assert.Nil(ctx.QueryValues("other"))
		return ctx.End(http.StatusNoContent)
	})
	app.UseHandler(r)

	srv := app.Start()
	defer srv.Close()

	host := "http://" + srv.Addr().String()
	req := NewRequst()
	res, err := req.Get(host + "/api?type=user&id=123")
	assert.Nil(err)
	assert.Equal(204, res.StatusCode)

	req = NewRequst()
	res, err = req.Get(host + "/view?id=123&id=abc")
	assert.Nil(err)
	assert.Equal(204, res.StatusCode)
}
