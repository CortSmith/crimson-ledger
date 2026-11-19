package window

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
)

// lowLevel is what a real web view offers; bindAsync builds the non-blocking binding on top of it.
type lowLevel struct {
	bind func(name string, fn any) error
	init func(js string)
	eval func(js string) // must be safe from any goroutine
}

var errorType = reflect.TypeOf((*error)(nil)).Elem()

// bindAsync exposes fn as a promise-returning JS function that never blocks the UI thread. The page-side wrapper picks an id,
// calls a tiny native stub that only starts a goroutine, and resolves when that goroutine evals window.__goDone(id, ok, json).
func bindAsync(l lowLevel, name string, fn any) error {
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func || v.Type().NumOut() > 2 {
		return fmt.Errorf("bind %q: not a func returning a value and optionally an error", name)
	}
	if v.Type().IsVariadic() {
		return fmt.Errorf("bind %q: variadic funcs are not supported", name)
	}
	stub := func(id int, raw ...json.RawMessage) (bool, error) {
		args, err := decodeArgs(v, raw)
		if err != nil {
			return false, err
		}
		go func() {
			res, err := call(v, args)
			if err == nil {
				var b []byte
				if b, err = json.Marshal(res); err == nil {
					l.eval("window.__goDone(" + strconv.Itoa(id) + ",true," + string(b) + ")")
					return
				}
			}
			b, _ := json.Marshal(err.Error())
			l.eval("window.__goDone(" + strconv.Itoa(id) + ",false," + string(b) + ")")
		}()
		return true, nil
	}
	if err := l.bind("__go_"+name, stub); err != nil {
		return err
	}
	l.init(asyncShim(name))
	return nil
}

func decodeArgs(v reflect.Value, raw []json.RawMessage) ([]reflect.Value, error) {
	t := v.Type()
	if len(raw) != t.NumIn() {
		return nil, errors.New("function arguments mismatch")
	}
	args := make([]reflect.Value, len(raw))
	for i := range raw {
		a := reflect.New(t.In(i))
		if err := json.Unmarshal(raw[i], a.Interface()); err != nil {
			return nil, err
		}
		args[i] = a.Elem()
	}
	return args, nil
}

func call(v reflect.Value, args []reflect.Value) (any, error) {
	res := v.Call(args)
	switch len(res) {
	case 0:
		return nil, nil
	case 1:
		if res[0].Type().Implements(errorType) {
			if e, _ := res[0].Interface().(error); e != nil {
				return nil, e
			}
			return nil, nil
		}
		return res[0].Interface(), nil
	}
	if e, _ := res[1].Interface().(error); e != nil {
		return nil, e
	}
	return res[0].Interface(), nil
}

func asyncShim(name string) string {
	n, _ := json.Marshal(name)
	return `(function(){var w=window;if(!w.__goPending){w.__goPending={};w.__goSeq=0;w.__goDone=function(i,ok,v){var p=w.__goPending[i];delete w.__goPending[i];if(p)(ok?p.res:p.rej)(v)};}` +
		`var n=` + string(n) + `;w[n]=function(){var a=Array.prototype.slice.call(arguments),id=++w.__goSeq;` +
		`return new Promise(function(res,rej){w.__goPending[id]={res:res,rej:rej};w["__go_"+n].apply(null,[id].concat(a)).catch(function(e){delete w.__goPending[id];rej(e)})})}})();`
}
