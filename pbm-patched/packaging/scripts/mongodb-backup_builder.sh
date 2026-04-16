#!/bin/sh

shell_quote_string() {
    echo "$1" | sed -e 's,\([^a-zA-Z0-9/_.=-]\),\\\1,g'
}

usage() {
    cat <<EOF
Usage: $0 [OPTIONS]
    The following options may be given :
        --builddir=DIR      Absolute path to the dir where all actions will be performed
        --get_sources       Source will be downloaded from github
        --build_src_rpm     If it is set - src rpm will be built
        --build_src_deb  If it is set - source deb package will be built
        --build_rpm         If it is set - rpm will be built
        --build_deb         If it is set - deb will be built
        --build_tarball     If it is set - tarball will be built
        --install_deps      Install build dependencies(root privilages are required)
        --branch            Branch for build
        --repo              Repo for build
        --version           Version to build

        --help) usage ;;
Example $0 --builddir=/tmp/percona-backup-mongodb --get_sources=1 --build_src_rpm=1 --build_rpm=1
EOF
    exit 1
}

append_arg_to_args() {
    args="$args "$(shell_quote_string "$1")
}

parse_arguments() {
    pick_args=
    if test "$1" = PICK-ARGS-FROM-ARGV; then
        pick_args=1
        shift
    fi

    for arg; do
        val=$(echo "$arg" | sed -e 's;^--[^=]*=;;')
        case "$arg" in
        --builddir=*) WORKDIR="$val" ;;
        --build_src_rpm=*) SRPM="$val" ;;
        --build_src_deb=*) SDEB="$val" ;;
        --build_rpm=*) RPM="$val" ;;
        --build_deb=*) DEB="$val" ;;
        --get_sources=*) SOURCE="$val" ;;
        --build_tarball=*) TARBALL="$val" ;;
        --branch=*) BRANCH="$val" ;;
        --repo=*) REPO="$val" ;;
        --version=*) VERSION="$val" ;;
        --install_deps=*) INSTALL="$val" ;;
        --help) usage ;;
        *)
            if test -n "$pick_args"; then
                append_arg_to_args "$arg"
            fi
            ;;
        esac
    done
}

check_workdir() {
    if [ "x$WORKDIR" = "x$CURDIR" ]; then
        echo >&2 "Current directory cannot be used for building!"
        exit 1
    else
        if ! test -d "$WORKDIR"; then
            echo >&2 "$WORKDIR is not a directory."
            exit 1
        fi
    fi
    return
}

get_sources() {
    cd "${WORKDIR}"
    if [ "${SOURCE}" = 0 ]; then
        echo "Sources will not be downloaded"
        return 0
    fi
    PRODUCT=percona-backup-mongodb
    echo "PRODUCT=${PRODUCT}" >percona-backup-mongodb.properties
    echo "BUILD_NUMBER=${BUILD_NUMBER}" >>percona-backup-mongodb.properties
    echo "BUILD_ID=${BUILD_ID}" >>percona-backup-mongodb.properties
    echo "VERSION=${VERSION}" >>percona-backup-mongodb.properties
    echo "BRANCH=${BRANCH}" >>percona-backup-mongodb.properties
    git clone "$REPO"
    retval=$?
    if [ $retval != 0 ]; then
        echo "There were some issues during repo cloning from github. Please retry one more time"
        exit 1
    fi
    cd percona-backup-mongodb
    if [ ! -z "$BRANCH" ]; then
        git reset --hard
        git clean -xdf
        git checkout "$BRANCH"
    fi
    REVISION=$(git rev-parse --short HEAD)
    GITCOMMIT=$(git rev-parse HEAD 2>/dev/null)
    GITBRANCH=$(git rev-parse --abbrev-ref HEAD 2>/dev/null)
    echo "VERSION=${VERSION}" >VERSION
    echo "REVISION=${REVISION}" >>VERSION
    echo "GITCOMMIT=${GITCOMMIT}" >>VERSION
    echo "GITBRANCH=${GITBRANCH}" >>VERSION
    echo "REVISION=${REVISION}" >>${WORKDIR}/percona-backup-mongodb.properties
    rm -fr debian rpm
    cd ${WORKDIR}

    mv percona-backup-mongodb ${PRODUCT}-${VERSION}
    tar --owner=0 --group=0 --exclude=.* -czf ${PRODUCT}-${VERSION}.tar.gz ${PRODUCT}-${VERSION}
    echo "UPLOAD=UPLOAD/experimental/BUILDS/${PRODUCT}/${PRODUCT}-${VERSION}/${BRANCH}/${REVISION}/${BUILD_ID}" >>percona-backup-mongodb.properties
    mkdir $WORKDIR/source_tarball
    mkdir $CURDIR/source_tarball
    cp ${PRODUCT}-${VERSION}.tar.gz $WORKDIR/source_tarball
    cp ${PRODUCT}-${VERSION}.tar.gz $CURDIR/source_tarball
    cd $CURDIR
    rm -rf percona-backup-mongodb
    return
}

get_system() {
    if [ -f /etc/redhat-release ]; then
        RHEL=$(rpm --eval %rhel)
        ARCH=$(echo $(uname -m) | sed -e 's:i686:i386:g')
        OS_NAME="el$RHEL"
        OS="rpm"
    elif [ -f /etc/amazon-linux-release ]; then
        RHEL=$(rpm --eval %amzn)
        ARCH=$(echo $(uname -m) | sed -e 's:i686:i386:g')
        OS_NAME="amzn$RHEL"
        OS="rpm"
    else
        ARCH=$(uname -m)
        OS_NAME="$(lsb_release -sc)"
        OS="deb"
    fi
    return
}

install_golang() {
    if [ x"$ARCH" = "xx86_64" ]; then
        GO_ARCH="amd64"
    elif [ x"$ARCH" = "xaarch64" ]; then
        GO_ARCH="arm64"
    fi
    GO_VERSION="1.25.3"
    GO_TAR="go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
    GO_URL="https://downloads.percona.com/downloads/packaging/go/${GO_TAR}"
    DL_PATH="/tmp/${GO_TAR}"
    for i in {1..3}; do
        wget -q "$GO_URL" -O "$DL_PATH" && break
        echo "Failed to download GOLang, retrying in 10 seconds..."
        sleep 10
    done
    tar --transform=s,go,go${GO_VERSION}, -zxf "$DL_PATH"
    rm -rf /usr/local/go*
    mv go${GO_VERSION} /usr/local/
    ln -s /usr/local/go${GO_VERSION} /usr/local/go
}

install_deps() {
    if [ $INSTALL = 0 ]; then
        echo "Dependencies will not be installed"
        return
    fi
    if [ ! $(id -u) -eq 0 ]; then
        echo "It is not possible to instal dependencies. Please run as root"
        exit 1
    fi
    CURPLACE=$(pwd)

    if [ "x$OS" = "xrpm" ]; then
        yum clean all
        if [ "x$RHEL" = "x10" ]; then
            yum -y install oracle-epel-release-el10
        fi
        INSTALL_LIST="epel-release git wget"
        if [ "x$RHEL" = "x2023" -o "x$RHEL" = "x10" ]; then
            INSTALL_LIST="git wget"
        fi
        yum -y install ${INSTALL_LIST}
        yum -y install rpm-build make rpmlint rpmdevtools golang krb5-devel
        install_golang
    else
        until apt-get update; do
            sleep 1
            echo "waiting"
        done
        DEBIAN_FRONTEND=noninteractive apt-get -y install lsb_release
        export DEBIAN=$(lsb_release -sc)
        export ARCH=$(echo $(uname -m) | sed -e 's:i686:i386:g')
        INSTALL_LIST="wget devscripts debhelper debconf pkg-config curl make golang git libkrb5-dev"
        until DEBIAN_FRONTEND=noninteractive apt-get -y install ${INSTALL_LIST}; do
            sleep 1
            echo "waiting"
        done
        install_golang
    fi
    return
}

get_tar() {
    TARBALL=$1
    TARFILE=$(basename $(find $WORKDIR/$TARBALL -name 'percona-backup-mongodb*.tar.gz' | sort | tail -n1))
    if [ -z $TARFILE ]; then
        TARFILE=$(basename $(find $CURDIR/$TARBALL -name 'percona-backup-mongodb*.tar.gz' | sort | tail -n1))
        if [ -z $TARFILE ]; then
            echo "There is no $TARBALL for build"
            exit 1
        else
            cp $CURDIR/$TARBALL/$TARFILE $WORKDIR/$TARFILE
        fi
    else
        cp $WORKDIR/$TARBALL/$TARFILE $WORKDIR/$TARFILE
    fi
    return
}

get_deb_sources() {
    param=$1
    echo $param
    FILE=$(basename $(find $WORKDIR/source_deb -name "percona-backup-mongodb*.$param" | sort | tail -n1))
    if [ -z $FILE ]; then
        FILE=$(basename $(find $CURDIR/source_deb -name "percona-backup-mongodb*.$param" | sort | tail -n1))
        if [ -z $FILE ]; then
            echo "There is no sources for build"
            exit 1
        else
            cp $CURDIR/source_deb/$FILE $WORKDIR/
        fi
    else
        cp $WORKDIR/source_deb/$FILE $WORKDIR/
    fi
    return
}

build_srpm() {
    if [ $SRPM = 0 ]; then
        echo "SRC RPM will not be created"
        return
    fi
    if [ "x$OS" = "xdeb" ]; then
        echo "It is not possible to build src rpm here"
        exit 1
    fi
    cd $WORKDIR
    get_tar "source_tarball"
    rm -fr rpmbuild
    ls | grep -v tar.gz | xargs rm -rf
    TARFILE=$(find . -name 'percona-backup-mongodb*.tar.gz' | sort | tail -n1)
    SRC_DIR=${TARFILE%.tar.gz}
    mkdir -vp rpmbuild/{SOURCES,SPECS,BUILD,SRPMS,RPMS}
    tar vxzf ${WORKDIR}/${TARFILE} --wildcards '*/packaging' --strip=1
    tar vxzf ${WORKDIR}/${TARFILE} --wildcards '*/VERSION' --strip=1
    source VERSION
    #
    sed -e "s:@@VERSION@@:${VERSION}:g" \
        -e "s:@@RELEASE@@:${RELEASE}:g" \
        -e "s:@@REVISION@@:${REVISION}:g" \
        packaging/rpm/mongodb-backup.spec >rpmbuild/SPECS/mongodb-backup.spec
    mv -fv ${TARFILE} ${WORKDIR}/rpmbuild/SOURCES
    rpmbuild -bs --define "_topdir ${WORKDIR}/rpmbuild" --define "version ${VERSION}" --define "dist .generic" rpmbuild/SPECS/mongodb-backup.spec
    mkdir -p ${WORKDIR}/srpm
    mkdir -p ${CURDIR}/srpm
    cp rpmbuild/SRPMS/*.src.rpm ${CURDIR}/srpm
    cp rpmbuild/SRPMS/*.src.rpm ${WORKDIR}/srpm
    return
}

build_rpm() {
    if [ $RPM = 0 ]; then
        echo "RPM will not be created"
        return
    fi
    if [ "x$OS" = "xdeb" ]; then
        echo "It is not possible to build rpm here"
        exit 1
    fi
    SRC_RPM=$(basename $(find $WORKDIR/srpm -name 'percona-backup-mongodb*.src.rpm' | sort | tail -n1))
    if [ -z $SRC_RPM ]; then
        SRC_RPM=$(basename $(find $CURDIR/srpm -name 'percona-backup-mongodb*.src.rpm' | sort | tail -n1))
        if [ -z $SRC_RPM ]; then
            echo "There is no src rpm for build"
            echo "You can create it using key --build_src_rpm=1"
            exit 1
        else
            cp $CURDIR/srpm/$SRC_RPM $WORKDIR
        fi
    else
        cp $WORKDIR/srpm/$SRC_RPM $WORKDIR
    fi
    cd $WORKDIR
    rm -fr rpmbuild
    mkdir -vp rpmbuild/{SOURCES,SPECS,BUILD,SRPMS,RPMS}
    cp $SRC_RPM rpmbuild/SRPMS/

    echo "RHEL=${RHEL}" >>percona-backup-mongodb.properties
    echo "ARCH=${ARCH}" >>percona-backup-mongodb.properties
    [[ ${PATH} == *"/usr/local/go/bin"* && -x /usr/local/go/bin/go ]] || export PATH=/usr/local/go/bin:${PATH}
    export GOROOT="/usr/local/go/"
    export GOPATH=$(pwd)/
    export PATH="/usr/local/go/bin:$PATH:$GOPATH"
    export GOBINPATH="/usr/local/go/bin"
    #fi
    rpmbuild --define "_topdir ${WORKDIR}/rpmbuild" --define "dist .$OS_NAME" --rebuild rpmbuild/SRPMS/$SRC_RPM

    return_code=$?
    if [ $return_code != 0 ]; then
        exit $return_code
    fi
    mkdir -p ${WORKDIR}/rpm
    mkdir -p ${CURDIR}/rpm
    cp rpmbuild/RPMS/*/*.rpm ${WORKDIR}/rpm
    cp rpmbuild/RPMS/*/*.rpm ${CURDIR}/rpm
}

build_source_deb() {
    if [ $SDEB = 0 ]; then
        echo "source deb package will not be created"
        return
    fi
    if [ "x$OS" = "xrmp" ]; then
        echo "It is not possible to build source deb here"
        exit 1
    fi
    rm -rf percona-backup-mongodb*
    get_tar "source_tarball"
    rm -f *.dsc *.orig.tar.gz *.debian.tar.gz *.changes
    #
    TARFILE=$(basename $(find . -name 'percona-backup-mongodb*.tar.gz' | sort | tail -n1))
    DEBIAN=$(lsb_release -sc)
    ARCH=$(echo $(uname -m) | sed -e 's:i686:i386:g')
    tar zxf ${TARFILE}
    BUILDDIR=${TARFILE%.tar.gz}
    #
    rm -fr ${BUILDDIR}/debian
    cp -av ${BUILDDIR}/packaging/debian ${BUILDDIR}
    #
    mv ${TARFILE} ${PRODUCT}_${VERSION}.orig.tar.gz
    cd ${BUILDDIR}
    source VERSION
    cp -r packaging/debian ./
    sed -i "s:@@VERSION@@:${VERSION}:g" debian/rules
    sed -i "s:@@REVISION@@:${REVISION}:g" debian/rules
    sed -i "s:sysconfig:default:" packaging/conf/pbm-agent.service
    dch -D unstable --force-distribution -v "${VERSION}-${RELEASE}" "Update to new MongoDB-Backup version ${VERSION}"
    dpkg-buildpackage -S
    cd ../
    mkdir -p $WORKDIR/source_deb
    mkdir -p $CURDIR/source_deb
    cp *.debian.tar.* $WORKDIR/source_deb
    cp *_source.changes $WORKDIR/source_deb
    cp *.dsc $WORKDIR/source_deb
    cp *.orig.tar.gz $WORKDIR/source_deb
    cp *.diff.gz $WORKDIR/source_deb
    cp *.debian.tar.* $CURDIR/source_deb
    cp *_source.changes $CURDIR/source_deb
    cp *.dsc $CURDIR/source_deb
    cp *.orig.tar.gz $CURDIR/source_deb
    cp *.diff.gz $CURDIR/source_deb
}

build_deb() {
    if [ $DEB = 0 ]; then
        echo "Binary deb package will not be created"
        return
    fi
    if [ "x$OS" = "xrmp" ]; then
        echo "It is not possible to build binary deb here"
        exit 1
    fi
    for file in 'dsc' 'orig.tar.gz' 'changes' 'diff.gz'; do
        get_deb_sources $file
    done
    cd $WORKDIR
    rm -fv *.deb
    #
    export DEBIAN=$(lsb_release -sc)
    export ARCH=$(echo $(uname -m) | sed -e 's:i686:i386:g')
    #
    echo "DEBIAN=${DEBIAN}" >>percona-backup-mongodb.properties
    echo "ARCH=${ARCH}" >>percona-backup-mongodb.properties

    #
    DSC=$(basename $(find . -name '*.dsc' | sort | tail -n1))
    #
    dpkg-source -x ${DSC}
    #
    cd ${PRODUCT}-${VERSION}
    source VERSION
    sed -i "s:@@VERSION@@:${VERSION}:g" debian/rules
    sed -i "s:@@REVISION@@:${REVISION}:g" debian/rules

    dch -m -D "${DEBIAN}" --force-distribution -v "${VERSION}-${RELEASE}.${DEBIAN}" 'Update distribution'

    export PATH=/usr/local/go/bin:${PATH}
    export GOROOT="/usr/local/go/"
    export GOPATH=$(pwd)/build
    export PATH="/usr/local/go/bin:$PATH:$GOPATH"
    export GO_BUILD_LDFLAGS="-w -s -X main.version=${VERSION} -X main.commit=${REVISION}"
    export GOBINPATH="/usr/local/go/bin"

    dpkg-buildpackage -rfakeroot -us -uc -b
    mkdir -p $CURDIR/deb
    mkdir -p $WORKDIR/deb
    cp $WORKDIR/*.deb $WORKDIR/deb
    cp $WORKDIR/*.deb $CURDIR/deb
}

build_tarball() {
    if [ $TARBALL = 0 ]; then
        echo "Binary tarball will not be created"
        return
    fi
    get_tar "source_tarball"
    cd $WORKDIR
    TARFILE=$(basename $(find . -name 'percona-backup-mongodb*.tar.gz' | sort | tail -n1))

    if [ -f /etc/debian_version ]; then
        export OS_RELEASE="$(lsb_release -sc)"
        export DEBIAN_VERSION="$(lsb_release -sc)"
        export DEBIAN="$(lsb_release -sc)"
    fi
    #
    if [ -f /etc/redhat-release ]; then
        export OS_RELEASE="centos$(rpm --eval %rhel)"
        RHEL=$(rpm --eval %rhel)
    fi
    #
    ARCH=$(uname -m 2>/dev/null || true)
    TARFILE=$(basename $(find . -name 'percona-backup-mongodb*.tar.gz' | sort | grep -v "tools" | tail -n1))
    PSMDIR=${TARFILE%.tar.gz}
    PSMDIR_ABS=${WORKDIR}/${PSMDIR}

    tar xzf $TARFILE
    rm -f $TARFILE
    mkdir -p build/src/github.com/percona/percona-backup-mongodb
    mv ${PSMDIR}/* build/src/github.com/percona/percona-backup-mongodb/
    export PATH=/usr/local/go/bin:${PATH}
    export GOROOT="/usr/local/go/"
    export GOPATH=${PWD}/build
    export PATH="/usr/local/go/bin:${PATH}:${GOPATH}"
    export GOBINPATH="/usr/local/go/bin"

    cd build/src/github.com/percona/percona-backup-mongodb/
    source VERSION
    export VERSION
    export GITBRANCH
    export GITCOMMIT
    make build-all
    cp ./bin/pbm ${WORKDIR}/${PSMDIR}/
    cp ./bin/pbm-agent ${WORKDIR}/${PSMDIR}/
    cp ./bin/pbm-speed-test ${WORKDIR}/${PSMDIR}/
    cp ./bin/pbm-agent-entrypoint ${WORKDIR}/${PSMDIR}/
    cd ${WORKDIR}/

    tar --owner=0 --group=0 -czf ${WORKDIR}/${PSMDIR}-${ARCH}.tar.gz ${PSMDIR}
    DIRNAME="tarball"
    mkdir -p ${WORKDIR}/${DIRNAME}
    mkdir -p ${CURDIR}/${DIRNAME}
    cp ${WORKDIR}/${PSMDIR}-${ARCH}${TARBALL_SUFFIX}.tar.gz ${WORKDIR}/${DIRNAME}
    cp ${WORKDIR}/${PSMDIR}-${ARCH}${TARBALL_SUFFIX}.tar.gz ${CURDIR}/${DIRNAME}
}

#main

CURDIR=$(pwd)
VERSION_FILE=$CURDIR/percona-backup-mongodb.properties
args=
WORKDIR=
SRPM=0
SDEB=0
RPM=0
DEB=0
SOURCE=0
TARBALL=0
OS_NAME=
ARCH=
OS=
INSTALL=0
RPM_RELEASE=1
DEB_RELEASE=1
VERSION="1.0.0"
RELEASE="1"
REVISION=0
BRANCH="nocoord"
REPO="https://github.com/percona/percona-backup-mongodb.git"
PRODUCT=percona-backup-mongodb
parse_arguments PICK-ARGS-FROM-ARGV "$@"
PSM_BRANCH=${BRANCH}

check_workdir
get_system
install_deps
get_sources
build_tarball
build_srpm
build_source_deb
build_rpm
build_deb
