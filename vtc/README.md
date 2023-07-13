# This section contains a few basic tests.

# Files

## Legacy files

The files:

```
i001.vtc
i002.vtc
i003.vtc
```

were included in the original version of the program this program is based on. I never used them, but I kept them here because somebody else might find them useful.

## Broadcaster testing suite

The files:

```
caches.ini
default.vcl
docker-compose.yml
test.txt
```

are part of the basic testing setup I used.

# Usage

Execute the following docker command to start the testing containers:

```
$ sudo docker-compose up -d
```

Upon successfull start-up, you'll have a bunch of varnishes running using the *default.vcl* configuration. A container running the broadcaster will also be started. The varnishes are configured to accept a **BAN** command. Send a **BAN** command to the broadcaster and see what happens:

```
$ curl -X BAN http://localhost:9999/
```

You can view the output as follows:

```
$ docker logs -f <broadcaster_container_name_or_id>
```

Try changing the caches.ini configuration and send a Hang-UP signal to the broadcaster to reload its configuration file:

```
$ docker kill --signal="HUP" <broadcaster_container_name_or_id>
```

This is a good starting point. You can expand the tests as much or as little as you need.

# Load testing

A easy way to perform some load-testing is to use Apache Bench (or ab - install separately):

```
$ ab -n 100000 -c 250 -m BAN http://localhost:9999/
```

This command will start 100000 requests with a parallelism of 250.
