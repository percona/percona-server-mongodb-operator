# Percona Server for MongoDB Operator

[![Build Status](https://travis-ci.org/percona/percona-server-mongodb-operator.svg?branch=master)](https://travis-ci.org/percona/percona-server-mongodb-operator)
[![Go Report Card](https://goreportcard.com/badge/github.com/percona/percona-server-mongodb-operator)](https://goreportcard.com/report/github.com/percona/percona-server-mongodb-operator)
[![FOSSA Status](https://app.fossa.io/api/projects/git%2Bgithub.com%2Fpercona%2Fpercona-server-mongodb-operator.svg?type=shield)](https://app.fossa.io/projects/git%2Bgithub.com%2Fpercona%2Fpercona-server-mongodb-operator?ref=badge_shield)

A Kubernetes operator for [Percona Server for MongoDB](https://www.percona.com/software/mongo-database/percona-server-for-mongodb) based on the [Operator SDK](https://github.com/operator-framework/operator-sdk).

# Documentation
See the [Official Documentation](https://percona.github.io/percona-server-mongodb-operator/) for more information.

[![Official Documentation](https://via.placeholder.com/260x60/419bdc/FFFFFF/?text=Documentation)](https://percona.github.io/percona-server-mongodb-operator/)

# DISCLAIMER

**This code is incomplete, expect major issues and changes until this repo has stabilised!**

## Submitting Bug Reports

If you find a bug in Percona Docker Images or in one of the related projects, please submit a report to that project's [JIRA](https://jira.percona.com) issue tracker.

Your first step should be [search](https://jira.percona.com/issues/?jql=project%20%3D%20%22Cloud%20Dev%22)  for a similar report in the existing set of open tickets. If someone else has already reported your problem, upvote that report to increase its visibility.

If there is no existing report, submit a report following these steps:

1. [Sign in to Percona JIRA.](https://jira.percona.com/login.jsp) You will need to create an account if you do not have one.
2. [Go to the Create Issue screen and select the relevant project.](https://jira.percona.com/secure/CreateIssueDetails!init.jspa?pid=12500&issuetype=1&priority=3)
3. Fill in the fields of Summary, Description, Steps To Reproduce, and Affects Version to the best you can. If the bug corresponds to a crash, attach the stack trace from the logs.

An excellent resource is [Elika Etemad's article on filing good bug reports.](http://fantasai.inkedblade.net/style/talks/filing-good-bugs/).

As a general rule of thumb, please try to create bug reports that are:

- *Reproducible.* Include steps to reproduce the problem.
- *Specific.* Include as much detail as possible: which version, what environment, etc.
- *Unique.* Do not duplicate existing tickets.
- *Scoped to a Single Bug.* One bug per report.


## License
[![FOSSA Status](https://app.fossa.io/api/projects/git%2Bgithub.com%2Fpercona%2Fpercona-server-mongodb-operator.svg?type=large)](https://app.fossa.io/projects/git%2Bgithub.com%2Fpercona%2Fpercona-server-mongodb-operator?ref=badge_large)