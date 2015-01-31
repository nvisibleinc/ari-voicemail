ari-voicemail
=============
Simple voicemail application using the ARI interface and the Go programming
language.

This application is designed to work with the [go-ari-proxy][1] and
[go-ari-library][2], and should not be considered ready for production. Please
feel free to use this application as a basis of learning and prototyping.

Installation
------------
```go
$ go build ari-voicemail
```

Usage
-----
First, build the `go-ari-proxy` application and configure the json file. We
recommend that you use the [NATS][3] message bus. Alternatively, you could
implement your own proxy for connecting to the Asterisk REST Interface either
in your preferred programming lanaguage, or in Go using our handy
[go-ari-library][2].

Licensing
---------
> Copyright 2014 N-Visible Technology Lab, Inc.
> 
> This program is free software; you can redistribute it and/or
> modify it under the terms of the GNU General Public License
> as published by the Free Software Foundation; either version 2
> of the License, or (at your option) any later version.
> 
> This program is distributed in the hope that it will be useful,
> but WITHOUT ANY WARRANTY; without even the implied warranty of
> MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
> GNU General Public License for more details.
> 
> You should have received a copy of the GNU General Public License
> along with this program; if not, write to the Free Software
> Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301, USA.

	[1]: https://github.com/nvisibleinc/go-ari-proxy
	[2]: https://github.com/nvisibleinc/go-ari-library
	[3]: https://github.com/derekcollison/nats