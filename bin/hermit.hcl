env = {
  "GOEXPERIMENT": "synctest",
  "GOFLAGS": "--tags=mysql,postgres,sqlite",
  "PATH": "${HERMIT_ENV}/scripts:${PATH}",
}

github-token-auth {
}
