package app

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"syscall"

	"github.com/moycat/shiba/model"
	"github.com/moycat/shiba/util"
	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

func (shiba *Shiba) getAPIContext() (context.Context, func()) {
	if shiba.apiTimeout > 0 {
		return context.WithTimeout(context.Background(), shiba.apiTimeout)
	}
	return context.WithCancel(context.Background())
}

func (shiba *Shiba) getNodeTunnelName(nodeName string) string {
	shiba.nodeMapLock.Lock()
	defer shiba.nodeMapLock.Unlock()
	if shiba.nodeMapCache != nil && len(shiba.nodeMapCache[nodeName]) > 0 {
		return shiba.nodeMapCache[nodeName]
	}
	return tunnelPrefix + util.NewUID()
}

func (shiba *Shiba) isTunnelInSync(link *netlink.Ip6tnl, node *model.Node) bool {
	if link.LinkAttrs.Flags|net.FlagUp == 0 {
		log.Debugf("tunnel [%s] is not up", link.Name)
		return false
	}
	if !link.Local.Equal(shiba.nodeIP) || !link.Remote.Equal(node.IP) {
		log.Debugf("tunnel [%s] has bad peer config", link.Name)
		return false
	}
	addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		log.Errorf("failed to get addr list of tunnel [%s]: %v", link.Name, err)
		return false
	}
	addrMap := make(map[string]bool)
	for _, addr := range addrs {
		if addr.Scope != syscall.RT_SCOPE_UNIVERSE {
			continue
		}
		if ones, bits := addr.Mask.Size(); ones != bits {
			log.Debugf("tunnel [%s] has non-single address [%v]", link.Name, addr.IPNet.String())
		}
		addrMap[addr.IP.String()] = true
	}
	if !reflect.DeepEqual(addrMap, shiba.nodeGatewayMap) {
		log.Debugf("tunnel [%s] has bad ips: %v", link.Name, addrMap)
		return false
	}
	return true
}

func (shiba *Shiba) loadNodeMapCache() {
	path := filepath.Join(os.TempDir(), nodeMapCacheFilename)
	f, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Errorf("failed to open node map file [%s] for reading: %v", path, err)
		}
		return
	}
	defer func() { _ = f.Close() }()
	decoder := json.NewDecoder(f)
	cache := new(model.NodeMapCache)
	if err := decoder.Decode(&cache); err != nil {
		log.Errorf("failed to unmarshal node map cache file [%s]: %v", path, err)
		return
	}
	if cache.Version != nodeMapCacheVersion {
		log.Warning("node map cache version does not match, ignoring")
		return
	}
	shiba.nodeMapLock.Lock()
	shiba.nodeMapCache = cache.Mapping
	shiba.nodeMapLock.Unlock()
}

func (shiba *Shiba) dumpNodeMapCache() {
	path := filepath.Join(os.TempDir(), nodeMapCacheFilename)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		log.Errorf("failed to open node map file [%s] for writing: %v", path, err)
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Errorf("failed to close node map file [%s]: %v", path, err)
		}
	}()
	encoder := json.NewEncoder(f)
	nodeMap := shiba.cloneNodeMap()
	cache := &model.NodeMapCache{
		Version: nodeMapCacheVersion,
		Mapping: make(map[string]string),
	}
	for name, node := range nodeMap {
		cache.Mapping[name] = node.Tunnel
	}
	if err := encoder.Encode(cache); err != nil {
		log.Errorf("failed to marshal node map cache to [%s]: %v", path, err)
		return
	}
	shiba.nodeMapLock.Lock()
	shiba.nodeMapCache = cache.Mapping
	shiba.nodeMapLock.Unlock()
}

func (shiba *Shiba) cloneNodeMap() model.NodeMap {
	shiba.nodeMapLock.Lock()
	nodeMap := shiba.nodeMap
	shiba.nodeMapLock.Unlock()
	newNodeMap := make(model.NodeMap, len(nodeMap))
	for k, v := range nodeMap {
		node := *v
		newNodeMap[k] = &node
	}
	return newNodeMap
}

func (shiba *Shiba) saveNodeMap(nodeMap model.NodeMap) {
	shiba.nodeMapLock.Lock()
	shiba.nodeMap = nodeMap
	shiba.nodeMapLock.Unlock()
}
