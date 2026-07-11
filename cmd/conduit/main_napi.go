//go:build napi

package main

/*
#include <stdlib.h>
#include <node_api.h>

extern napi_value conduitStart(napi_env env, napi_callback_info info);
extern napi_value conduitStop(napi_env env, napi_callback_info info);
extern napi_value conduitReload(napi_env env, napi_callback_info info);

static napi_status conduit_define(napi_env env, napi_value exports,
                                  const char *name, napi_callback cb) {
  napi_value fn;
  napi_status status =
      napi_create_function(env, name, NAPI_AUTO_LENGTH, cb, NULL, &fn);
  if (status != napi_ok) {
    return status;
  }
  return napi_set_named_property(env, exports, name, fn);
}

static napi_status conduit_define_all(napi_env env, napi_value exports) {
  napi_status status = conduit_define(env, exports, "start", conduitStart);
  if (status != napi_ok) {
    return status;
  }
  status = conduit_define(env, exports, "stop", conduitStop);
  if (status != napi_ok) {
    return status;
  }
  return conduit_define(env, exports, "reload", conduitReload);
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"sync"
	"unsafe"

	"github.com/pycabbage/conduit/internal/jsonc"
	"github.com/pycabbage/conduit/internal/relay"
)

var (
	mgrMu sync.Mutex
	mgr   *relay.Manager
)

func parseConfigs(configJSONC string) ([]relay.BotConfig, error) {
	var cfgs []relay.BotConfig
	if err := json.Unmarshal(jsonc.ToJSON([]byte(configJSONC)), &cfgs); err != nil {
		return nil, err
	}
	return cfgs, nil
}

func jsUndefined(env C.napi_env) C.napi_value {
	var undef C.napi_value
	if C.napi_get_undefined(env, &undef) != C.napi_ok {
		return nil
	}
	return undef
}

func jsString(env C.napi_env, s string) C.napi_value {
	cstr := C.CString(s)
	defer C.free(unsafe.Pointer(cstr))
	var result C.napi_value
	if C.napi_create_string_utf8(env, cstr, C.size_t(len(s)), &result) != C.napi_ok {
		return jsUndefined(env)
	}
	return result
}

func firstStringArg(env C.napi_env, info C.napi_callback_info) (string, bool) {
	argc := C.size_t(1)
	var argv [1]C.napi_value
	if C.napi_get_cb_info(env, info, &argc, &argv[0], nil, nil) != C.napi_ok {
		return "", false
	}
	if argc < 1 {
		return "", false
	}

	var length C.size_t
	if C.napi_get_value_string_utf8(env, argv[0], nil, 0, &length) != C.napi_ok {
		return "", false
	}

	buf := C.malloc(length + 1)
	if buf == nil {
		return "", false
	}
	defer C.free(buf)

	var copied C.size_t
	if C.napi_get_value_string_utf8(env, argv[0], (*C.char)(buf), length+1, &copied) != C.napi_ok {
		return "", false
	}
	return C.GoStringN((*C.char)(buf), C.int(copied)), true
}

func applyStart(configJSON string) error {
	cfgs, err := parseConfigs(configJSON)
	if err != nil {
		return err
	}

	mgrMu.Lock()
	prev := mgr
	mgr = relay.NewManager()
	next := mgr
	mgrMu.Unlock()

	if prev != nil {
		prev.StopAll()
	}
	next.Apply(cfgs)
	return nil
}

func applyReload(configJSON string) error {
	mgrMu.Lock()
	m := mgr
	mgrMu.Unlock()
	if m == nil {
		return fmt.Errorf("conduit: reload called before start")
	}

	cfgs, err := parseConfigs(configJSON)
	if err != nil {
		return err
	}
	m.Apply(cfgs)
	return nil
}

//export conduitStart
func conduitStart(env C.napi_env, info C.napi_callback_info) C.napi_value {
	configJSON, ok := firstStringArg(env, info)
	if !ok {
		return jsString(env, "conduit: start requires a config JSON string argument")
	}
	if err := applyStart(configJSON); err != nil {
		return jsString(env, err.Error())
	}
	return jsString(env, "")
}

//export conduitStop
func conduitStop(env C.napi_env, _ C.napi_callback_info) C.napi_value {
	mgrMu.Lock()
	m := mgr
	mgrMu.Unlock()
	if m != nil {
		m.StopAll()
	}
	return jsUndefined(env)
}

//export conduitReload
func conduitReload(env C.napi_env, info C.napi_callback_info) C.napi_value {
	configJSON, ok := firstStringArg(env, info)
	if !ok {
		return jsString(env, "conduit: reload requires a config JSON string argument")
	}
	if err := applyReload(configJSON); err != nil {
		return jsString(env, err.Error())
	}
	return jsString(env, "")
}

//export napi_register_module_v1
func napi_register_module_v1(env C.napi_env, exports C.napi_value) C.napi_value {
	if C.conduit_define_all(env, exports) != C.napi_ok {
		C.napi_throw_error(env, nil, C.CString("conduit: failed to register module exports"))
	}
	return exports
}

func main() {}
