**CHANGE DESCRIPTION**
---
**Problem:**
*Short explanation of the problem.*

**Cause:**
*Short explanation of the root cause of the issue if applicable.*

**Solution:**
*Short explanation of the solution we are providing with this PR.*

**CHECKLIST**
---
**Jira**
- [ ] Is Jira ticket created and referenced properly?
- [ ] Does Jira ticket have proper status for documentation (`Needs Doc`) and QA (`Needs QA`)?
- [ ] Does Jira ticket link to proper `Fix Version` milestone?

**E2E tests**
- [ ] Are E2E tests passing?
- [ ] Are unit tests passing?
- [ ] Are linting tests passing?
- [ ] Is an E2E test/test case added for the new feature/change?
- [ ] Are unit tests added where appropriate?
- [ ] Are OpenShift compare files changed for E2E tests (`compare/*-oc.yml`)?

**Config/Logging/Testability**
- [ ] Are all needed new/changed options added to default yaml files?
- [ ] Are the manifests (crd/bundle) regenerated if needed?
- [ ] Did we add proper logging messages for operator actions?
- [ ] Did we NOT break compatibility with the previous version or cluster upgrade process?
- [ ] Does the change support oldest and newest supported MongoDB version?
- [ ] Does the change support oldest and newest supported Kubernetes version?
