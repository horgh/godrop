# godrop
This is an IRC client package. It is mainly useful for creating bots.


## Adding functionality
You can add functionality to clients via packages.

Packages can add to `godrop.Hooks` via an `init()` function. `godrop` calls
each hook with every IRC protocol message. This means you can take actions
based on anything that occurs on IRC.

This repository includes these packages to add functionality:


### `duckduckgo`
This package makes the client respond to `!trigger` type commands on channels
to query [DuckDuckGo](https://duckduckgo.com).

  * `!ddg`/`.ddg` search and show the first 4 results
  * `!ddg1`/`.ddg1` search and show the first result
  * `!duck`/`.duck` search for an [instant
    answers](https://duckduckgo.com/api)


### `oper`
This package makes the client an IRC operator upon connect. You need to
define `oper-name` and `oper-password` in your client's configuration to
use it.


### `recordips`
This package causes the client to record connecting IPs to a file. It's
based on [ircd-ratbox](http://ratbox.org/) notices. The bot must be an
operator to see these notices.
