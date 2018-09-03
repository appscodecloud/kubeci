#!/bin/bash
set -xe

GOPATH=$(go env GOPATH)
REPO_ROOT="$GOPATH/src/kube.ci/kubeci"

pushd $REPO_ROOT

# http://redsymbol.net/articles/bash-exit-traps/
function cleanup() {
  rm -rf $ONESSL ca.crt ca.key server.crt server.key
}
trap cleanup EXIT

# https://stackoverflow.com/a/677212/244009
if [[ ! -z "$(command -v onessl)" ]]; then
  export ONESSL=onessl
else
  # ref: https://stackoverflow.com/a/27776822/244009
  case "$(uname -s)" in
    Darwin)
      curl -fsSL -o onessl https://github.com/kubepack/onessl/releases/download/0.6.0/onessl-darwin-amd64
      chmod +x onessl
      export ONESSL=./onessl
      ;;

    Linux)
      curl -fsSL -o onessl https://github.com/kubepack/onessl/releases/download/0.6.0/onessl-linux-amd64
      chmod +x onessl
      export ONESSL=./onessl
      ;;

    CYGWIN* | MINGW32* | MSYS*)
      curl -fsSL -o onessl.exe https://github.com/kubepack/onessl/releases/download/0.6.0/onessl-windows-amd64.exe
      chmod +x onessl.exe
      export ONESSL=./onessl.exe
      ;;
    *)
      echo 'other OS'
      ;;
  esac
fi

export KUBECI_NAMESPACE=default
export KUBE_CA=$($ONESSL get kube-ca | $ONESSL base64)
export KUBECI_ENABLE_WEBHOOK=true
export KUBECI_E2E_TEST=false

while test $# -gt 0; do
  case "$1" in
    -n)
      shift
      if test $# -gt 0; then
        export KUBECI_NAMESPACE=$1
      else
        echo "no namespace specified"
        exit 1
      fi
      shift
      ;;
    --namespace*)
      shift
      if test $# -gt 0; then
        export KUBECI_NAMESPACE=$1
      else
        echo "no namespace specified"
        exit 1
      fi
      shift
      ;;
    --enable-webhook*)
      val=$(echo $1 | sed -e 's/^[^=]*=//g')
      if [ "$val" = "false" ]; then
        export KUBECI_ENABLE_WEBHOOK=false
      fi
      shift
      ;;
    --test*)
      val=$(echo $1 | sed -e 's/^[^=]*=//g')
      if [ "$val" = "true" ]; then
        export KUBECI_E2E_TEST=true
      fi
      shift
      ;;
    *)
      echo $1
      exit 1
      ;;
  esac
done

# !!! WARNING !!! Never do this in prod cluster
kubectl create clusterrolebinding serviceaccounts-cluster-admin --clusterrole=cluster-admin --user=system:anonymous || true

cat $REPO_ROOT/hack/dev/apiregistration.yaml | envsubst | kubectl apply -f -

#if [ "$KUBECI_ENABLE_WEBHOOK" = true ]; then
#  cat $REPO_ROOT/hack/deploy/validating-webhook.yaml | envsubst | kubectl apply -f -
#fi

$REPO_ROOT/hack/make.py

if [ "$KUBECI_E2E_TEST" = false ]; then # don't run operator while run this script from test
kubeci run \
    --secure-port=6443 \
    --kubeconfig="$HOME/.kube/config" \
    --authorization-kubeconfig="$HOME/.kube/config" \
    --authentication-kubeconfig="$HOME/.kube/config" \
    --authentication-skip-lookup
fi

popd
