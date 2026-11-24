# From the bothy-lando-migrate skill, reference set 1.
#
# Local development only: compose.dev.yml builds this.  QA, CI and production
# don't use it; they build the app's own Dockerfile, which this file leaves
# alone.  It is the FrankenPHP base image plus what development needs, and
# the app's code is not copied in: compose.dev.yml mounts the project folder
# at /app.
#
# When the app's own Dockerfile moves to the FrankenPHP base too, it has a
# `dev` stage that does the same job: point compose.dev.yml at that and bin
# this file.

### PHP version we are targetting
ARG PHP_VERSION=8.4

# already runs as www-data, listens on :6060 and has /app as the workdir
FROM uogsoe/soe-php-frankenphp:${PHP_VERSION}

#- php/ldap configs (COPY runs as root, so system paths are fine)
COPY docker/ldap.conf /etc/ldap/ldap.conf
COPY docker/custom_php.ini /usr/local/etc/php/conf.d/custom_php.ini

#- php re-reads a file when it changes (custom_php.ini turns that off)
COPY docker/recheck_files.ini /usr/local/etc/php/conf.d/zz_recheck_files.ini

#- pcov (code coverage) is compiled into the base image but switched off
USER root
RUN docker-php-ext-enable pcov
USER www-data
