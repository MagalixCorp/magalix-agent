package opa_auditor

import "github.com/MagalixCorp/magalix-agent/v3/agent"

type AuditResultsCache struct {
	cache map[string]map[string]agent.AuditResultStatus
}

func NewAuditResultsCache() *AuditResultsCache {
	return &AuditResultsCache{cache:make(map[string]map[string]agent.AuditResultStatus)}
}

func (c *AuditResultsCache) Put(constraintId string, resourceId string, status agent.AuditResultStatus) {
	constraint, found := c.cache[constraintId]
	if !found {
		constraint = make(map[string]agent.AuditResultStatus)
		c.cache[constraintId] = constraint
	}
	constraint[resourceId] = status
}

func (c *AuditResultsCache) Get(constraintId string, resourceId string) (agent.AuditResultStatus, bool) {
	constraint, found := c.cache[constraintId]
	if !found {
		return "", false
	}
	status, found := constraint[resourceId]
	return status, found
}

func (c *AuditResultsCache) RemoveConstraint(constraintId string) {
	delete(c.cache, constraintId)
}

func (c *AuditResultsCache) RemoveResource(resourceId string) {
	for _, constraint := range c.cache {
		delete(constraint, resourceId)
	}
}