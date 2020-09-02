module github.com/MagalixCorp/magalix-agent/v2

go 1.13

replace github.com/MagalixTechnologies/core/logger => /home/khaled/code/github.com/MagalixTechnologies/core/logger

require (
	github.com/MagalixTechnologies/alltogether-go v0.0.0-20181206150142-f01ae5621759
	github.com/MagalixTechnologies/channel v1.1.0
	github.com/MagalixTechnologies/core/logger v1.0.2
	github.com/MagalixTechnologies/log-go v0.0.0-20191209143418-aff8f3a92a31
	github.com/MagalixTechnologies/uuid-go v0.0.0-20191003092420-742176f3bcb7
	github.com/docopt/docopt-go v0.0.0-20180111231733-ee0de3bc6815
	github.com/evanphx/json-patch v4.2.0+incompatible // indirect
	github.com/golang/snappy v0.0.1
	github.com/kovetskiy/lorg v0.0.0-20190701130800-9c6042b7edb0 // indirect
	github.com/pkg/errors v0.8.1
	github.com/reconquest/cog v0.0.0-20191208202052-266c2467b936 // indirect
	github.com/reconquest/health-go v0.0.0-20181113092653-ea90ecace101
	github.com/reconquest/karma-go v0.0.0-20190930125156-7b5c19ad6eab
	github.com/reconquest/sign-go v0.0.0-20181113092801-8d4f8c5854ae
	github.com/reconquest/stats-go v0.0.0-20180307085907-df9f297af353
	github.com/ryanuber/go-glob v1.0.0
	github.com/satori/go.uuid v1.2.1-0.20181028125025-b2ce2384e17b
	github.com/zazab/zhash v0.0.0-20170403032415-ad45b89afe7a // indirect
	golang.org/x/net v0.0.0-20191209160850-c0dbc17a3553
	golang.org/x/oauth2 v0.0.0-20191202225959-858c2ad4c8b6 // indirect
	golang.org/x/sync v0.0.0-20190911185100-cd5d95a43a6e
	golang.org/x/time v0.0.0-20191024005414-555d28b269f0 // indirect
	k8s.io/api v0.18.8
	k8s.io/apimachinery v0.18.8
	k8s.io/client-go v0.18.8
)
