#include <node_api.h>
#include <stdlib.h>

#include "libconduitcore.h"

static char *conduit_string_arg(napi_env env, napi_callback_info info) {
  size_t argc = 1;
  napi_value argv[1];
  napi_get_cb_info(env, info, &argc, argv, NULL, NULL);

  size_t length = 0;
  napi_status status =
      napi_get_value_string_utf8(env, argv[0], NULL, 0, &length);

  char *buf = malloc(length + 1);
  if (status != napi_ok) {
    buf[0] = '\0';
    return buf;
  }

  size_t copied;
  napi_get_value_string_utf8(env, argv[0], buf, length + 1, &copied);
  return buf;
}

static napi_value conduit_result(napi_env env, char *err) {
  napi_value result;
  if (err == NULL) {
    napi_create_string_utf8(env, "", 0, &result);
    return result;
  }
  napi_create_string_utf8(env, err, NAPI_AUTO_LENGTH, &result);
  free(err);
  return result;
}

static napi_value Start(napi_env env, napi_callback_info info) {
  char *configJSON = conduit_string_arg(env, info);
  char *err = ConduitStart(configJSON);
  free(configJSON);
  return conduit_result(env, err);
}

static napi_value Stop(napi_env env, napi_callback_info info) {
  ConduitStop();
  napi_value undefined;
  napi_get_undefined(env, &undefined);
  return undefined;
}

static napi_value Reload(napi_env env, napi_callback_info info) {
  char *configJSON = conduit_string_arg(env, info);
  char *err = ConduitReload(configJSON);
  free(configJSON);
  return conduit_result(env, err);
}

static napi_value conduit_define(napi_env env, napi_value exports,
                                  const char *name, napi_callback cb) {
  napi_value fn;
  napi_create_function(env, name, NAPI_AUTO_LENGTH, cb, NULL, &fn);
  napi_set_named_property(env, exports, name, fn);
  return exports;
}

NAPI_MODULE_INIT() {
  conduit_define(env, exports, "start", Start);
  conduit_define(env, exports, "stop", Stop);
  conduit_define(env, exports, "reload", Reload);
  return exports;
}
