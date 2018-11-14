# Overview
Magalix provides unique insights and recommendations about resources utilization inside Kubernetes. Magalix Autopilot right sizes Kubernetes cluster by dynamically managing resources of pods and containers. Save up to 50% of your cloud bill. Stay on top of capacity management. 

### Core Insights
- What is the distribution of CPU, memory, and network across the whole cluster? 
- How will utilization look like the next few hours?
- Are there any unusual usage patterns?
- How does the change in cluster size impacts performance?

### Core Magalix Autopilot Features
- Control when and how optimization is done
- Automatically apply optimizations with minimal or no disruption to operations
- Gain detailed recommendations to right size cluster VMs

# Documentation

The general documentation of the Magalix and its agent, including instructions for installation and dashboards, is located under the [docs website](https://docs.magalix.com).

# Installation
A valid account is required to run the agent properly.

1. Go to [https://console.magalix.com](https://console.magalix.com) to create an account.
2. Copy and paste the provided Kubectl command into your shell. 
3. It will install Magalix agent with the proper credentials to read your metrics and generate recommendations. 

**Notes**

* Your first cluster analytics are free.
* No changes will be applied till you turn on the autopilot feature at the [console](https://console.magalix.com)
* The Autopilot feature is a Pro feature. You need to buy a subscription to enable it. 

# Troubleshooting 

The most common issues that users face installing Magalix agent is RBAC. Please read our [troubelshooting guide](https://docs.magalix.com/docs/connecting-clusters) to resolve initial setup issues you may encounter. 

# Questions and Support
Please reach out to us at our [support forum](https://docs.magalix.com/discuss), or send us an email at <support@magalix.com>

