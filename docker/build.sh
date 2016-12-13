#!/bin/bash
BUILD_DATE=$(date -u '+%Y%m%d_%H_%M_%S')
BUILD_VERSION=$(git describe --always)
VERSION=${BUILD_VERSION}
PACKAGE_DIR="${GOPATH}/src/github.com/nitro/haproxy-api/"
BUILD_DOCKER_DIR="${PACKAGE_DIR}/docker/"
die() {
	echo $1
	exit 1
}

get_deps() {
  if [[ ! $(which github-release) ]]; then
  go get github.com/aktau/github-release
  fi
}

release_notes() {
  rm  ${BUILD_DOCKER_DIR}/release-notes.txt
  touch ${BUILD_DOCKER_DIR}/release-notes.txt
  file ${PACKAGE_DIR}/haproxy-api > ${BUILD_DOCKER_DIR}/release-notes.txt
  openssl md5 ${PACKAGE_DIR}/haproxy-api >> ${BUILD_DOCKER_DIR}/release-notes.txt
  echo "GIT Tag or Hash:${BUILD_VERSION}" >> ${BUILD_DOCKER_DIR}/release-notes.txt
}

build() {
  cd ${PACKAGE_DIR}
  GOOS="linux" godep go build -ldflags="-X main.version=${VERSION}" && echo "build seems to have run..."
  release_notes
  cat ${BUILD_DOCKER_DIR}/release-notes.txt
  test -f ${PACKAGE_DIR}/haproxy-api && file ${PACKAGE_DIR}/haproxy-api | grep "ELF.*LSB" || die "haproxy-api is missing or not a Linux binary"
  test -f ${PACKAGE_DIR}/haproxy-api.toml && cp ${PACKAGE_DIR}/haproxy-api.toml ${BUILD_DOCKER_DIR}
  cp ${PACKAGE_DIR}/haproxy-api ${BUILD_DOCKER_DIR} && cp -pr ${PACKAGE_DIR}/templates ${BUILD_DOCKER_DIR} || die "Failed to copy"
}

publish_docker() {
  cd ${BUILD_DOCKER_DIR}
  echo ${DOCKER_HOST}
  docker build -q -t gonitro/haproxy-api:${VERSION} -f Dockerfile .  > last_build || die "Failed to build container"
  cat last_build
  # docker push gonitro/haproxy-api:${BUILD_VERSION}
}

release_github() {
  echo "weelease the seecwat veapon! ${VERSION}"
}

release_docker() {
  echo "pushing docker image ${VERSION}"

}

build_inside_docker() {
 cd ${PACKAGE_DIR}
 docker build -t haproxy-api-build \
 --build-arg DOCKER_HOST=192.168.168.167:2376 \
 --build-arg VERSION=3142 \
 --build-arg GITHUB_TOKEN=${GITHUB_TOKEN} \
 -f docker/Dockerfile.release .
 echo "now you might want to run: docker run -it --rm haproxy-api"
}

clean_workspace() {
cd ${PACKAGE_DIR}
rm 	haproxy-api docker/haproxy-api.toml	docker/last_build docker/release-notes.txt release-notes.txt
}


case ${1} in
clean)
  clean_workspace
  ;;
buildindocker)
  build_inside_docker
  ;;
build)
  build || die "build died oops."
  publish_docker || die "publish docker died oops."
  ;;
release)
  release_github
  release_docker
  ;;
*)
  echo "Usage: ${0} { build | release }"
  exit 1
  ;;
esac
