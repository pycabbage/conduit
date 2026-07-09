//go:build napi

// Node-API (N-API) entrypoint for conduit, built as a native Node addon
// (`.node`) with `-buildmode=c-shared -tags=napi`. See
// docs/adr/0009-native-napi-addon-instead-of-wasm.md for the rationale
// (native shared library instead of wasm, no Go->JS callback bridge in v1).
//
// This wraps the exact same relay.Manager (NewManager/Apply/StopAll) as the
// native CLI in main.go; only the entrypoint differs. Because the shared
// library runs on real OS threads, net.Dial / coder/websocket blocking reads /
// goroutine scheduling all work unchanged (unlike the wasm build).
//
// BUILD REQUIREMENT: node_api.h (Node-API public header) must be reachable on
// the C include path at build time. Provide it via the CGO_CFLAGS environment
// variable pointing at the include directory of the `node-api-headers` npm
// package (distributed by the Node.js project) or a Node.js installation, e.g.:
//
//	CGO_ENABLED=1 \
//	CGO_CFLAGS="-I/path/to/node-api-headers/include" \
//	go build -tags=napi -buildmode=c-shared -o conduit.node ./cmd/conduit
//
// Do NOT hardcode an include path here or in any committed build script. (While
// developing in this repo's environment the headers happened to be available at
// /nix/store/.../nodejs-slim-24.16.0/include/node, but that is an
// environment-specific path and must never be baked into the build.)

package main

/*
#include <stdlib.h>
#include <node_api.h>

// Go callbacks exported below via //export. They must be forward-declared here
// (not defined) so the preamble stays declaration-only, which is required when
// a file uses //export. The static helper functions below are fine in the
// preamble because `static` gives them internal linkage (no duplicate symbols).
extern napi_value conduitStart(napi_env env, napi_callback_info info);
extern napi_value conduitStop(napi_env env, napi_callback_info info);
extern napi_value conduitReload(napi_env env, napi_callback_info info);

// conduit_define registers a single JS function `name` -> `cb` on `exports`.
// napi_create_function needs a C function pointer; taking the address of a Go
// //export'd symbol is only legal from C, hence this small C helper.
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

// conduit_define_all wires up start/stop/reload on the module exports object.
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

// One relay per process. A second start() replaces (and stops) the first; this
// mirrors the wasm build (main_js.go) and is intentional (see ADR 0009).
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

// jsUndefined returns the JS `undefined` value, used as the return of a
// void-returning callback. Falls back to NULL (which Node also treats as
// undefined) if the call fails.
func jsUndefined(env C.napi_env) C.napi_value {
	var undef C.napi_value
	if C.napi_get_undefined(env, &undef) != C.napi_ok {
		return nil
	}
	return undef
}

// jsString creates a JS string from a Go string.
func jsString(env C.napi_env, s string) C.napi_value {
	cstr := C.CString(s)
	defer C.free(unsafe.Pointer(cstr))
	var result C.napi_value
	if C.napi_create_string_utf8(env, cstr, C.size_t(len(s)), &result) != C.napi_ok {
		return jsUndefined(env)
	}
	return result
}

// firstStringArg reads the first callback argument as a UTF-8 Go string using
// the standard two-call N-API pattern: query the required byte length first,
// then allocate and copy.
func firstStringArg(env C.napi_env, info C.napi_callback_info) (string, bool) {
	argc := C.size_t(1)
	var argv [1]C.napi_value
	if C.napi_get_cb_info(env, info, &argc, &argv[0], nil, nil) != C.napi_ok {
		return "", false
	}
	if argc < 1 {
		return "", false
	}

	// First call: buf == NULL, bufsize == 0 -> length receives the byte count
	// (excluding the terminating NUL).
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

// applyStart parses configJSON and (re)starts the relay, replacing any existing
// manager. Returns an error, if any, for the caller to surface to JS.
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

// applyReload parses configJSON and applies it to the existing manager.
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
	// Empty string signals success (see ADR 0009: errors are reported through
	// start()'s synchronous return value, not a JS callback).
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

// main is required for `package main`. It is never executed when the package is
// built with -buildmode=c-shared (the addon is driven entirely through the
// exported N-API functions above); it exists so plain `go build -tags=napi`
// also succeeds.
func main() {}
