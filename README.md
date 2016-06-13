HAproxy-API
===========

This application is a companion piece to
[Sidecar](https://github.com/newrelic/sidecar) that allows you to run HAproxy
and Sidecar in separate Docker containers that can be deployed separately. This
has the advantage of not taking down HAproxy while redeploying Sidecar. It is
not a general purpose API and relies heavily on the encoding of the state used
by Sidecar

This application will manage HAproxy by either running it or restarting it
after templating out a configuration from the provided Sidecar state.

Configuration
-------------

The application itself is not configurable except by passing in a command
line switch to tell it which configuration file to use. That file contains
settings that only really pertain to HAproxy itself. Most of the items
are self-explanatory.

```
bind_ip       = "0.0.0.0"
template_file = "views/haproxy.cfg"
config_file   = "/etc/haproxy.cfg"
pid_file      = "/var/run/haproxy.pid"
```

Health Checking
---------------

`haproxy-api` can be health checked by sending a `GET` request to the `/health`
endpoint. This in turn simply checks to make sure that HAproxy is currently
running by shelling out to `bash`, `ps`, and `grep`.

Contributing
------------

Contributions are more than welcome. Bug reports with specific reproduction steps are great. If you have a code contribution you'd like to make, open a pull request with suggested code.

Pull requests should:

 * Clearly state their intent in the title
 * Have a description that explains the need for the changes
 * Include tests!
 * Not break the public API

Ping us to let us know you're working on something interesting by opening a GitHub Issue on the project.
