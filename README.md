# Magalix Agent - Overview [![Tweet](https://img.shields.io/twitter/url/http/shields.io.svg?style=social)](https://twitter.com/intent/tweet?text=Run%20kubernetes%20clusters%20on%20autopilot%20&url=https://www.magalix.com/&via=MagalixCorp&hashtags=Kubernetes,Cloud,SRE,DevOps)



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

## Directly through Magalix.com 

1. Go to [https://console.magalix.com](https://console.magalix.com) to create an account.
2. Copy and paste the provided Kubectl command into your shell. 
3. It will install Magalix agent with the proper credentials to read your metrics and generate recommendations. 

## Via GKE Marketplace

You can install Magalix agent through GKE marketplace. 

### First Time User
1. Insert your email in GKE installation/configuration form. The deployment container will create an account and connect the agent to this account.
2. Once installation is successfully complete, you will receive an email with instructions to see your clusters dashboard at [Magalix console](https://console.magalix.com)
3. If you didn't receive that welcome email for some reason, you can just go thorugh the [reset password process](https://console.magalix.com/auth/#/forgot-password).

### If you have an Existing Magalix Account
1. Insert your email and password in GKE installation/configuration form. The deployment container will define a new cluster under your account and use generated secrets to connect installed agent with your account. 
2. Once installation is successfully complete, you will receive an email confirming cluster connectivity.

**Notes**

* Your first cluster analytics are free.
* No recommendations will be applied to your cluster till you turn on the Autopilot feature at the [console](https://console.magalix.com)
* The Autopilot feature is a Pro feature. You need to buy a subscription to enable it. 

# Accessing Insights and Recommendations
Few minutes after agent installation, metrics will start to flow. Magalix analytics and recommendations engine will generate predictions and recommendations in few hours. You will also receive email notifications when recommendations are generated.

![Few snapshots of recommenations, resources distributions, and namespace resources timeseries](https://github.com/MagalixCorp/magalix-agent/blob/master/pics/snapshots-decision-distribution-timeseries-ns-shadow.png "Generated Decision and Resources Distribution")

# Get Slack Notifications
You can add slack webhook to receive notifications when a container or the cluster is having issues or when recommendations are generated. Go to [cluster's dashboard](https://console.magalix.com/) and click on the watch icon. 

![How to watch a cluster](https://github.com/MagalixCorp/magalix-agent/blob/master/pics/snapshots-watch-cluster.png "Watch cluster popup")

**Note**
Your first cluster watch feature is enabled by default. It will send you email only notifications. 

# Updating The Agent's Image
If you need to update the running agent's installation, you will receive email that you should do. Because the image pull policy is set to Always, everytime you delete the pod, a fresh image will be installed. 

# Removing Magalix Agent

You can remove Magalix agent by simply deleting its Deployment controller, which is named magalix-agent. This will remove all the agent's pods and associated resources. 

# Troubleshooting 

The most common issues that users face installing Magalix agent is RBAC. Please read our [troubelshooting guide](https://docs.magalix.com/docs/connecting-clusters) to resolve initial setup issues you may encounter. 

# Questions and Support
Please reach out to us at our [support forum](https://docs.magalix.com/discuss), or send us an email at <support@magalix.com>

