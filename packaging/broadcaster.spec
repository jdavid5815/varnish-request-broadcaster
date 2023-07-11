Name: broadcaster
Summary: Request distribution tool.
Release: 1%{?dist}
Group: Application/Tools
License: GPL
Version: 1.0
Vendor: Varnish Software
Source0: %{expand:%%(pwd)}
URL: https://www.varnish-software.com/
BuildRequires: go
Requires(pre): /usr/sbin/useradd, /usr/bin/getent
Requires(post): /sbin/chkconfig

%description
This package provides a broadcaster tool, a daemon that
spawns a http server and futher broadcasts requests
to various configured Varnish nodes.

%prep
# Create directory structure
mkdir -p %{_builddir}/usr/lib/systemd/system/
mkdir -p %{_builddir}/etc/broadcaster
mkdir -p %{_builddir}/usr/sbin
echo "Source is %{SOURCEURL0}"

cp %{SOURCEURL0}/caches.ini %{_builddir}/etc/broadcaster/caches.ini
cp %{SOURCEURL0}/broadcaster %{_builddir}/usr/sbin/broadcaster
cp %{SOURCEURL0}/broadcaster.service  %{_builddir}/usr/lib/systemd/system/broadcaster.service


%install
rm -rf %{buildroot}
cp -r %{_builddir} %{buildroot}
exit 0


%files
%config(noreplace) /etc/broadcaster/caches.ini
%attr(0755,root,root) /usr/sbin/broadcaster
%attr(0755,root,root) /usr/lib/systemd/system/broadcaster.service

%pre
# Create user and group
/usr/bin/getent group vcbc > /dev/null || /usr/sbin/groupadd -r vcbc
/usr/bin/getent passwd vcbc > /dev/null || /usr/sbin/useradd -r -g vcbc -s /sbin/nologin vcbc


%clean
rm -rf %{_builddir}