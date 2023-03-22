# ezsvclb

ezsvclb is a Kubernetes controller that automatically creates a load balancer for a service.
It is heavily inspired by k3s servicelb and allow you to "advertise" node IPs
where the target pods are running on. 

Unlike k3s servicelb, it DOES NOT effectively do anything on its own! It simply advertises
the IPs in the service's status so that externaldns and other tools that rely on this information
can be used with your cluster. To effectively route traffic to your pods you will need
to use `hostPort` or even `hostNetwork`.

Thanks to k3s servicelb for the inspiration and a significant part of the code that
has been adapted for ezsvclb! 

## Getting Started

This controller can be deployed using kustomize. You can deploy it using either
the `ezsvclb` folder or the `ezsvclb-default` folder. The difference is that `ezsvclb` only
pick up services with the `ezsvclb` [Load Balancer Class](https://kubernetes.io/docs/concepts/services-networking/service/#load-balancer-class). Whereas with `ezsvclb-default`
will also pick up services without a Load Balancer Class as well.

You can run the following command to deploy the controller:
```sh
kustomize build config/ezsvclb | kubectl apply -f -
```

Or this command if you want to use the default class:
```sh
kustomize build config/ezsvclb-default | kubectl apply -f -
```

Note that ingress-nginx Helm chart currently (03/2023) doesn't support using
Load Balancer Class so you will have to use `ezsvclb-default` in this case. If
you also happen to run on k3s you will have to disable `servicelb`
with `--disable=servicelb` in k3s.

## Annotations

- `ezsvclb.io/hostname: "<true|false>"`: Also reports node hostnames in the service load balancer
  status (disabled by default). This may be useful if you care about IPv6 support and you use
  externaldns. When hostname are provided externaldns will create CNAME records.
- `ezsvclb.io/hostname-suffix: "<string>"`: Append the specified text to the reported hostnames.
  This may be useful if you don't have fqdn for your node names. 
- `ezsvclb.io/ip: "<true|false>"`: Specifiy whether you want to report IPs in the service
  load balancer status (enabled by default).
- `ezsvclb.io/force-dual-stack: "<true|false>"`: Report both type of IPs regardless of the setting of the service.
  This may be useful for instance if your cluster doesn't support IPv6 but your hosts do
  and you use `hostNetwork`. Note that externaldns **DOES NOT** support IPv6 addresses in status though.

## License

Copyright 2023 Arthur Outhenin-Chalandre.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

