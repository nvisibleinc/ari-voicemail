ari-voicemail
=============
Simple voicemail application using the ARI interface and the Go programming
language.

This application is designed to work with the [go-ari-proxy][1] and
[go-ari-library][2], and should not be considered ready for production. Please
feel free to use this application as a basis of learning and prototyping.

Installation
------------
```
$ go build ari-voicemail
```

Usage
-----
First, build the `go-ari-proxy` application and configure the JSON file. We
recommend that you use the [NATS][3] message bus. Alternatively, you could
implement your own proxy for connecting to the Asterisk REST Interface either
in your preferred programming lanaguage, or in Go using our handy
[go-ari-library][2].

Licensing
---------
> Copyright 2015 N-Visible Technology Lab, Inc.
> 
> Licensed under the Apache License, Version 2.0 (the “License”); you may not
> use this file except in compliance with the License. You may obtain a copy
> of the License at
> 
> http://www.apache.org/licenses/LICENSE-2.0
> 
> Unless required by applicable law or agreed to in writing, software distributed
> under the License is distributed on an “AS IS” BASIS, WITHOUT WARRANTIES OR
> CONDITIONS OF ANY KIND, either express or implied. See the License for the
> specific language governing permissions and limitations under the License.

   [1]: https://github.com/nvisibleinc/go-ari-proxy "go-ari-proxy"
   [2]: https://github.com/nvisibleinc/go-ari-library "go-ari-library"
   [3]: https://github.com/apcera/gnatsd "NATS"
