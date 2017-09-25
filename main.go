package main

import (
	"fmt"
	"github.com/dghubble/sling"
	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/hashicorp/consul/api"
	"github.com/reportportal/commons-go/commons"
	"github.com/reportportal/commons-go/conf"
	"github.com/reportportal/commons-go/registry"
	"github.com/reportportal/commons-go/server"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

const proxyConsul string = "proxy_consul"

func main() {

	defaults := map[string]string{
		proxyConsul: "false",
	}
	cfg := conf.EmptyConfig()

	cfg.Consul.Address = "registry:8500"
	cfg.Consul.Tags = []string{
		"urlprefix-/",
		"traefik.frontend.rule=PathPrefix:/",
		"traefik.backend=index",
	}

	rpConf, err := conf.LoadConfig(cfg, defaults)
	if nil != err {
		log.Fatalf("Cannot load config %s", err.Error())
	}
	rpConf.AppName = "index"

	info := commons.GetBuildInfo()
	info.Name = "Service Index"

	srv := server.New(rpConf, info)

	srv.AddRoute(func(router *chi.Mux) {
		router.Use(middleware.Logger)
		router.NotFound(func(w http.ResponseWriter, rq *http.Request) {
			http.Redirect(w, rq, "/ui/404.html", http.StatusFound)
		})

		router.HandleFunc("/composite/info", func(w http.ResponseWriter, r *http.Request) {
			commons.WriteJSON(http.StatusOK, aggregateInfo(getNodesInfo(srv.Sd, true)), w)
		})
		router.HandleFunc("/composite/health", func(w http.ResponseWriter, r *http.Request) {
			commons.WriteJSON(http.StatusOK, aggregateHealth(getNodesInfo(srv.Sd, false)), w)
		})
		router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/ui/", http.StatusFound)
		})

		enableProxy, err := strconv.ParseBool(rpConf.Get(proxyConsul))
		if err != nil {
			enableProxy = false
		}

		if true == enableProxy {
			u, e := url.Parse("http://" + rpConf.Consul.Address)
			if e != nil {
				log.Fatal("Cannot parse consul URL")
			}

			proxy := httputil.NewSingleHostReverseProxy(u)
			router.Handle("/consul/*", http.StripPrefix("/consul/", proxy))
			router.Handle("/v1/*", proxy)
		}

	})
	srv.StartServer()
}

func parseKVTag(tags []string, tagsMap map[string]string) {
	for _, tag := range tags {
		kv := strings.Split(tag, "=")
		if 2 == len(kv) {
			tagsMap[kv[0]] = kv[1]
		}
	}
}

func aggregateHealth(nodesInfo map[string]*nodeInfo) map[string]interface{} {
	var aggregated = make(map[string]interface{}, len(nodesInfo))
	for node, info := range nodesInfo {
		var rs map[string]interface{}

		if "" != info.getHealthCheckURL() {
			_, e := sling.New().Base(info.BaseURL).Get(info.getHealthCheckURL()).Receive(&rs, &rs)
			if nil != e {
				rs = make(map[string]interface{}, 1)
				rs["status"] = "DOWN"
			}
		} else {
			rs = make(map[string]interface{}, 1)
			rs["status"] = "UNKNOWN"
		}

		aggregated[node] = rs
	}
	return aggregated
}

func aggregateInfo(nodesInfo map[string]*nodeInfo) map[string]interface{} {
	var aggregated = make(map[string]interface{}, len(nodesInfo))
	for node, info := range nodesInfo {
		var rs map[string]interface{}
		_, e := sling.New().Base(info.BaseURL).Get(info.getStatusPageURL()).ReceiveSuccess(&rs)
		if nil != e {
			log.Println(e)
			continue
		}
		if nil != rs {
			aggregated[node] = rs
		}

	}
	return aggregated
}

func getNodesInfo(discovery registry.ServiceDiscovery, passing bool) map[string]*nodeInfo {
	nodesInfo, _ := discovery.DoWithClient(func(client interface{}) (interface{}, error) {
		services, _, e := client.(*api.Client).Catalog().Services(&api.QueryOptions{})
		if nil != e {
			return nil, e
		}
		nodesInfo := make(map[string]*nodeInfo, len(services))
		for k := range services {
			instances, _, e := client.(*api.Client).Health().Service(k, "", passing, &api.QueryOptions{})
			if nil != e {
				return nil, e
			}
			for _, inst := range instances {
				tagsMap := map[string]string{}
				parseKVTag(inst.Service.Tags, tagsMap)

				var ni nodeInfo
				ni.BaseURL = fmt.Sprintf("http://%s:%d/", inst.Service.Address, inst.Service.Port)
				ni.Tags = tagsMap
				nodesInfo[strings.ToUpper(k)] = &ni
			}

		}

		return nodesInfo, nil
	})
	return nodesInfo.(map[string]*nodeInfo)
}

type nodeInfo struct {
	BaseURL string
	Tags    map[string]string
}

func (ni *nodeInfo) getStatusPageURL() string {
	return ni.Tags["statusPageUrlPath"]
}
func (ni *nodeInfo) getHealthCheckURL() string {
	return ni.Tags["healthCheckUrlPath"]
}
