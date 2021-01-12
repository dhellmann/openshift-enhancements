---
title: single-node-deployment-with-bootstrap-in-place
authors:
  - "@eranco"
  - "@mrunalp"
  - "@dhellmann"
  - "@romfreiman"
  - "@tsorya"
reviewers:
  - TBD
  - "@markmc"
  - "@deads2k"
  - "@wking"
  - "@eparis"
  - "@hexfusion"
approvers:
  - TBD
creation-date: 2020-12-13
last-updated: 2020-12-13
status: implementable
see-also:
  - https://github.com/openshift/enhancements/pull/560
  - https://github.com/openshift/enhancements/pull/302
---

# Single Node deployment with bootstrap-in-place

## Release Signoff Checklist

- [x] Enhancement is `implementable`
- [ ] Design details are appropriately documented from clear requirements
- [ ] Test plan is defined
- [ ] Graduation criteria for dev preview, tech preview, GA
- [ ] User-facing documentation is created in [openshift-docs](https://github.com/openshift/openshift-docs/)

## Summary

As we add support for new features such as [single-node production deployment](https://github.com/openshift/enhancements/pull/560/files),
we need a way to install such clusters without an extra node dependency for bootstrap.

This enhancement describes the flow for installing Single Node OpenShift using a liveCD that performs the bootstrap logic and reboots to become the single node.

## Motivation

Currently, all OpenShift installations use an auxiliary bootstrap node.
The bootstrap node creates a temporary control plane that is required for launching the actual cluster.

Single Node OpenShift installations will often be performed in environments where there are no extra nodes, so it is highly desirable to remove the need for a separate bootstrap machine to reduce the resources required to install the cluster.

The downsides of requiring a bootstrap node for Single Node OpenShift are:

1. The obvious additional node.
2. Requires external dependencies:
   1. Load balancer (only for bootstrap phase)
   2. Preconfigured DNS (for each deployment)
3. Cannot use Bare Metal IPI:
   1. Adds additional dependencies - VIPs, keepalived, mdns

### Goals

* Describe an approach for installing Single Node OpenShift in a BareMetal environment for production use.
* The implementation should require minimal changes to the OpenShift installer and the should not affect existing deployment flows.
* Installation should result a clean Single Node OpenShift without any bootstrap leftovers.
* Describe an approach that can be carried out by a user manually or automated by an orchestration tool.

### Non-Goals

* Addressing a similar installation flow for multi-node clusters.
* Single-node-developer (CRC) cluster-profile installation.
* Supporting cloud deployment for bootstrap in place. Using a live CD image is challenging in cloud environments, so this work is postponed to a future enhancement.

## Proposal

The OpenShift install process relies on an ephemeral bootstrap
environment so that none of the hosts in the running cluster end up
with unique configuration left over from computing how to create the
cluster. When the bootstrap virtual machine is removed from the
process, the temporary files, logs, etc. from that phase should still
be segregated from the "real" OpenShift files on the host. This means
it is useful to retain a "bootstrap environment", as long as we can
avoid requiring a separate host to run a virtual machine.

The focus for single-node deployments right now is edge use cases,
either for telco RAN deployments or other situations where a user may
have several instances being managed centrally. That means it is
important to make it possible to automate the workflow for deploying,
even if we also want to retain the option for users to deploy by hand.
In the telco RAN case, single-node deployments will be managed from a
central "hub" cluster using tools like RHACM, Hive, and metal3.

The baseboard management controller (BMC) in enterprise class hardware
can be given a URL to an ISO image and told to attach the image to the
host as though it was inserted into a CD-ROM or DVD drive. An image
booted from an ISO can use a ramdisk as a place to create temporary
files, without affecting the persistent storage in the host.  This
capability makes the existing live ISO for RHCOS a good foundation on
which to build this feature. A live ISO can serve as the "bootstrap
environment", separate from the real OpenShift system on persistent
storage in the host. The BMC in the host can be used to automate
deployment via a multi-cluster orchestration tool.

The RHCOS live ISO image uses Ignition to configure the host, just as
the RHCOS image used for running OpenShift does. This means Ignition
is the most effective way to turn an RHCOS live image into a
special-purpose image for performing the installation.

We propose the following steps for deploying single-node instances of
OpenShift:

1. Have the OpenShift installer generate a special Ignition config for
   a single-node deployment.
2. Combine that Ignition config with an RHCOS live ISO image to build
   an image for deploying OpenShift on a single node.
3. Boot the new image on the host.
4. Bootstrap the deployment, generating a new master Ignition config
   and the static pod definitions for OpenShift. Write them, along
   with an RHCOS image, to the disk in the host.
5. Reboot the host to the internal disk, discarding the ephemeral live
   image environment and allowing the previously generated artifacts
   to complete the installation and bring up OpenShift.

### User Stories

#### As an OpenShift user, I want to be able to deploy OpenShift on a supported single node configuration

A user will be able to run the OpenShift installer to create a single-node
deployment, with some limitations (see non-goals above). The user
will not require special support exceptions to receive technical assistance
for the features supported by the configuration.

### Implementation Details/Notes/Constraints

The installation images for single-node clusters will be unique for
each cluster. The user or orchestration tool will create an
installation image by combining the
`bootstrap-in-place-for-live-iso.ign` created by the installer with an
RHCOS live image using `coreos-install embed`. Making the image unique
allows us to build on the existing RHCOS live image, instead of
delivering a different base image, and means that the user does not
need any infrastructure to serve Ignition configs to hosts during
deployment.

In order to add a viable, working etcd post reboot, we will take a
snapshot of etcd and add it to the Ignition config for the host.
After rebooting, we will use the restored `etcd-member` from the
snapshot to rebuild the database. This allows etcd and the API service
to come up on the host without having to re-apply all of the
kubernetes operations run during bootstrapping.

#### OpenShift-installer

The OpenShift installer will be updated so that the `create
ignition-configs` command generates a new
`bootstrap-in-place-for-live-iso.ign` file when the number of replicas
for the control plane in the `install-config.yaml` is `1`.

This Ignition config will have a different `bootkube.sh` from the
default bootstrap Ignition. In addition to the standard rendering
logic, the modified script will:

1. Start `cluster-bootstrap` without required pods by setting `--required-pods=''`
2. Run `cluster-bootstrap` with the `--bootstrap-in-place` option.
3. Fetch the master Ignition and combine it with the original Ignition
   config, the control plane static pod manifests, the required
   kubernetes resources, and the bootstrap etcd database snapshot to
   create a new Ignition config for the host.
3. Write the RHCOS image and the combined Ignition config to disk.
4. Reboot the node.

#### Cluster-bootstrap

`cluster-bootstrap` will have a new entrypoint `--bootstrap-in-place`
which will get the master Ignition as input and will enrich the master
Ignition with control plane static pods manifests and all required
resources, including the etcd database.

`cluster-bootstrap` normally waits for a list of required pods to be
ready. These pods are expected to start running on the control plane
nodes when the bootstrap and control plane run in parallel. That is
not possible when bootstrapping in place, so when `cluster-bootstrap`
runs with the `--bootstrap-in-place` option it should only apply the
manifests and then tear down the control plane.

If `cluster-bootstrap` fails to apply some of the manifests, it should
return an error.

#### Bootstrap / Control plane static pods

The control plane components we will copy from
`/etc/kubernetes/manifests` into the master Ignition are:

1. etcd-pod
2. kube-apiserver-pod
3. kube-controller-manager-pod
4. kube-scheduler-pod

These components also require other files generated during bootstrapping:

1. `/var/lib/etcd`
2. `/etc/kubernetes/bootstrap-configs`
3. `/opt/openshift/tls/*` (`/etc/kubernetes/bootstrap-secrets`)
4. `/opt/openshift/auth/kubeconfig-loopback` (`/etc/kubernetes/bootstrap-secrets/kubeconfig`)

**Note**: `/etc/kubernetes/bootstrap-secrets` and `/etc/kubernetes/bootstrap-configs` will be deleted by the `post-reboot` service, after the node reboots (see below).

The bootstrap static pods will be generated in a way that the control
plane operators will be able to identify them and either continue in a
controlled way for the next revision, or just keep them as the correct
revision and reuse them.

#### Post-reboot service

We will add a new `post-reboot` service for approving the kubelet and
the node Certificate Signing Requests. This service will also cleanup
the bootstrap static pods resources when the OpenShift control plane
is ready.

Since we start with a liveCD, the bootstrap services (`bootkube`, `approve-csr`, etc.), `/etc` and `/opt/openshift` temporary files are written to the ephemeral filesystem of the live image, and not to the node's real filesystem.

The files that we need to delete are under:

* `/etc/kubernetes/bootstrap-secrets`
* `/etc/kubernetes/bootstrap-configs`

These files are required for the bootstrap control plane to start before it is replaced by the control plane operators.
Once the OCP control plane static pods are deployed we can delete the files as they are no longer required.

### Initial Proof-of-Concept

A proof-of-concept implementation is available for experimenting with
the design.

This POC uses the following services for mitigating some gaps:
- `patch.service` for allowing single node installation. This won't be required after [single-node production deployment](https://github.com/openshift/enhancements/pull/560) is implemented.
- `post_reboot.service` for approving the node CSR and bootstrap static pods resources cleanup post reboot.

To try it out:

1. Clone the installer branch: `iBIP_4_6` from https://github.com/eranco74/installer.git
2. Build the installer with the command: `TAGS=libvirt hack/build.sh`
3. Add your ssh key and pull secret to the `./install-config.yaml`
4. Generate the Ignition config with the command `make generate`
5. Set up DNS for `Cluster name: test-cluster, Base DNS: redhat.com` running: `make network`
6. Download an RHCOS live image and add the bootstrap Ignition config by running: `make embed`
7. Spin up a VM with the the liveCD with the command: `make start-iso`
8. Monitor the progress using `make ssh` and `journalctl -f -u bootkube.service` or `kubectl --kubeconfig ./mydir/auth/kubeconfig get clusterversion`

### Risks and Mitigations

*What are the risks of this proposal and how do we mitigate. Think broadly. For
example, consider both security and how this will impact the larger OKD
ecosystem.*

*How will security be reviewed and by whom? How will UX be reviewed and by whom?*

#### Custom Manifests for CRDs

One limitation of single-node deployments not present in multi-node
clusters is handling some custom resource definitions (CRDs). During
bootstrapping of a multi-node cluster, the bootstrap host and real
cluster hosts run in parallel for a time. This means that the
bootstrap host can iterate publishing manifests to the API server
until the operators running on the other hosts are up and define their
CRDs. If it takes a little while for those operators to install their
CRDs, the bootstrap host can wait and retry the operation. In a
single-node deployment, the bootstrap environment and real node are
not active at the same time. This means the bootstrap process may
block if it tries to create a custom resource using a CRD that is not
installed.

While most CRDs are created by the `cluster-version-operator`, some
CRDs are created later by the cluster operators. These CRDs from
cluster operators are not present during bootstrapping:

* clusternetworks.network.openshift.io
* controllerconfigs.machineconfiguration.openshift.io
* egressnetworkpolicies.network.openshift.io
* hostsubnets.network.openshift.io
* ippools.whereabouts.cni.cncf.io
* netnamespaces.network.openshift.io
* network-attachment-definitions.k8s.cni.cncf.io
* overlappingrangeipreservations.whereabouts.cni.cncf.io
* volumesnapshotclasses.snapshot.storage.k8s.io
* volumesnapshotcontents.snapshot.storage.k8s.io
* volumesnapshots.snapshot.storage.k8s.io

This limitation is unlikely to be triggered by manifests created by
the OpenShift installer, but we cannot control what extra manifests
users add to their deployment. Users need to be made aware of this
limitation and encouraged to avoid creating custom manifests using
CRDs installed by cluster operators instead of the
`cluster-version-operator`.

## Design Details

### Open Questions

1. How will the user specify custom configuration, such as installation disk, or static IPs?
2. Number of revisions for the control plane - do we want to make changes to the bootstrap static pods to make them closer to the final ones?

### Bootable installation artifact (future work)

In order to embed the bootstrap-in-place-for-live-iso Ignition config to the liveCD the user need to get the liveCD and the coreos-installer binary.
We consider adding `openshift-install create single-node-iso` command that that result a liveCD with the bootstrap-in-place-for-live-iso.ign embeded.
It can also take things like additional manifests for setting the RT kernel (and kernel args) via MachineConfig as well
 as supporting injecting network configuration as files and choosing the target disk drive for coreos-installer.
Internally, create single-node-iso would compile a single-node-iso-target.yaml into Ignition (much like coreos/fcct)
 and include it along with the Ignition it generates and embed it into the ISO.

### Test Plan

In order to claim full support for this configuration, we must have
CI coverage informing the release.
An end-to-end job using the bootstrap-in-place installation flow,
based on the [installer UPI CI](https://github.com/openshift/release/blob/master/ci-operator/templates/openshift/installer/cluster-launch-installer-metal-e2e.yaml#L507),
 running an appropriate subset of the standard OpenShift tests
will be created and configured to block accepting release images unless it passes.
This job is a different CI from the Single node production edge CI that will run with a bootstrap vm on cloud environment.

That end-to-end job should also be run against pull requests for
the  control plane repos, installer and cluster-bootstrap.

### Graduation Criteria

**Note:** *Section not required until targeted at a release.*

Define graduation milestones.

These may be defined in terms of API maturity, or as something else. Initial proposal
should keep this high-level with a focus on what signals will be looked at to
determine graduation.

Consider the following in developing the graduation criteria for this
enhancement:

- Maturity levels
    - [`alpha`, `beta`, `stable` in upstream Kubernetes][maturity-levels]
    - `Dev Preview`, `Tech Preview`, `GA` in OpenShift
- [Deprecation policy][deprecation-policy]

Clearly define what graduation means by either linking to the [API doc definition](https://kubernetes.io/docs/concepts/overview/kubernetes-api/#api-versioning),
or by redefining what graduation means.

In general, we try to use the same stages (alpha, beta, GA), regardless how the functionality is accessed.

[maturity-levels]: https://git.k8s.io/community/contributors/devel/sig-architecture/api_changes.md#alpha-beta-and-stable-versions
[deprecation-policy]: https://kubernetes.io/docs/reference/using-api/deprecation-policy/

#### Examples

##### Dev Preview -> Tech Preview

- Ability to utilize the enhancement end to end
- End user documentation, relative API stability
- Sufficient test coverage
- Gather feedback from users rather than just developers

##### Tech Preview -> GA

- More testing (upgrade, downgrade, scale)
- Sufficient time for feedback
- Available by default

**For non-optional features moving to GA, the graduation criteria must include
end to end tests.**

## Implementation History

Major milestones in the life cycle of a proposal should be tracked in `Implementation
History`.

## Drawbacks

1. The API will be unavailable from time to time during the installation.
2. Coreos-installer cannot be used in the cloud environment.

## Alternatives

### Installing using remote bootstrap node

Run the bootstrap node in a HUB cluster as VM.
This approach is appealing because it keeps the current installation flow.
Requires external dependencies.
However, there are drawbacks:
1. It will require Load balancer and DNS per installation.
2. Deployments run remotely via L3 connection (high latency (up to 150ms), low BW in some cases), this isn't optimal for etcd cluster (one member is running on the bootstrap during the installation)
3. Running the bootstrap on the HUB cluster present a (resources) scale issue (~50*(8GB+4cores)), limits ACM capacity

### Installing without liveISO

Run the bootstrap flow on the node disk and clean up all the bootstrap residues once the node fully configured.
This is very similar to the current enhancement installation approach but without the requirement to start from liveCD.
This approach advantage is that it will work on cloud environment.
The disadvantage is that it's more prune to result a single node deployment with bootstrap leftovers.


### Installing using a baked Ignition file.

The installer will generate an ignition config.
This Ignition configuration includes all assets required for launching the single node cluster (including TLS certificates and keys).
When booting a machine with CoreOS and this Ignition configuration the Ignition config will lay down the control plane operator static pods.
The ignition config will also create a static pod that functions as cluster-bootstrap (this pod should delete itself once it’s done) and apply the OCP assets to the control plane.

### Preserve etcd database instead of a snapshot

Another option for preserving the etcd database when pivoting from
bootstrap to production is to copy the entire database, instead of
using a snapshot operation.  When stopped, etcd will save its state
and exit. We can then add the `/var/lib/etcd` directory to the master
Ignition config.  After the reboot, etcd should start with all the
data it had prior to the reboot. By using a snapshot, instead of
saving the entire database, we will have more flexibility to change
the production etcd configuration before restoring the content of the
database.
