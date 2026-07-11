#include <signal.h>
#include <stdio.h>
#include <stdlib.h>

#include "libconduitcore.h"

static char *readConfigFile(const char *path) {
  FILE *f = fopen(path, "rb");
  if (f == NULL) {
    return NULL;
  }
  if (fseek(f, 0, SEEK_END) != 0) {
    fclose(f);
    return NULL;
  }
  long size = ftell(f);
  if (size < 0) {
    fclose(f);
    return NULL;
  }
  if (fseek(f, 0, SEEK_SET) != 0) {
    fclose(f);
    return NULL;
  }
  char *buf = malloc((size_t)size + 1);
  if (buf == NULL) {
    fclose(f);
    return NULL;
  }
  size_t n = fread(buf, 1, (size_t)size, f);
  fclose(f);
  buf[n] = '\0';
  return buf;
}

int main(void) {
  const char *configFile = getenv("CONFIG_FILE");
  if (configFile == NULL || configFile[0] == '\0') {
    configFile = "/etc/conduit/config.json";
  }

  sigset_t set;
  sigemptyset(&set);
  sigaddset(&set, SIGHUP);
  sigaddset(&set, SIGTERM);
  sigaddset(&set, SIGINT);
  sigprocmask(SIG_BLOCK, &set, NULL);

  int started = 0;

  char *data = readConfigFile(configFile);
  if (data == NULL) {
    fprintf(stderr, "config read: failed to read %s\n", configFile);
  } else {
    char *err = ConduitStart(data);
    free(data);
    if (err != NULL) {
      fprintf(stderr, "%s\n", err);
      free(err);
    } else {
      started = 1;
    }
  }

  for (;;) {
    int sig;
    if (sigwait(&set, &sig) != 0) {
      continue;
    }

    if (sig == SIGHUP) {
      fprintf(stderr, "SIGHUP: reloading config\n");
      char *reloadData = readConfigFile(configFile);
      if (reloadData == NULL) {
        fprintf(stderr, "config read: failed to read %s\n", configFile);
        continue;
      }
      char *err = started ? ConduitReload(reloadData) : ConduitStart(reloadData);
      free(reloadData);
      if (err != NULL) {
        fprintf(stderr, "%s\n", err);
        free(err);
      } else {
        started = 1;
      }
      continue;
    }

    ConduitStop();
    return 0;
  }
}
