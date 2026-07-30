import groovy.transform.Field

@Field def zone = 'us-central1-c'
@Field def testUrlPrefix = 'https://percona-jenkins-artifactory-public.s3.amazonaws.com/cloud-psmdb-operator'
@Field def tests = []
@Field def reportHtml = 'e2e-test-report.html'
@Field def reportXml = 'e2e-test-report.xml'
@Field int clusterCount = 15

void createCluster(String CLUSTER_SUFFIX) {
    withCredentials([string(credentialsId: 'GCP_PROJECT_ID', variable: 'GCP_PROJECT'), file(credentialsId: 'gcloud-key-file', variable: 'CLIENT_SECRET_FILE')]) {
        sh """
            export KUBECONFIG=/tmp/${CLUSTER_NAME}-${CLUSTER_SUFFIX}
            gcloud auth activate-service-account --key-file "\$CLIENT_SECRET_FILE"
            gcloud config set project "\$GCP_PROJECT"
            GKE_VERSION=\$(gcloud container get-server-config --zone ${zone} --flatten='channels[].validVersions[]' --filter='channels.channel=STABLE' --format='value(channels.validVersions)' | sort -V | head -n1)
            if [ -z "\${GKE_VERSION}" ]; then
                echo "Failed to detect the minimum Kubernetes version from the GKE stable release channel"
                exit 1
            fi
            ret_num=0
            while [ \${ret_num} -lt 15 ]; do
                ret_val=0
                gcloud container clusters list --filter ${CLUSTER_NAME}-${CLUSTER_SUFFIX} --zone ${zone} --format='csv[no-heading](name)' | xargs gcloud container clusters delete --zone ${zone} --quiet || true
                echo "Creating GKE cluster ${CLUSTER_NAME}-${CLUSTER_SUFFIX} with Kubernetes version \${GKE_VERSION} from the stable release channel"
                gcloud container clusters create ${CLUSTER_NAME}-${CLUSTER_SUFFIX} \
                    --spot \
                    --zone=${zone} \
                    --machine-type='n1-standard-4' \
                    --cluster-version="\${GKE_VERSION}" \
                    --num-nodes=3 \
                    --labels='delete-cluster-after-hours=6' \
                    --disk-size=30 \
                    --network=jenkins-vpc \
                    --subnetwork=jenkins-${CLUSTER_SUFFIX} \
                    --cluster-ipv4-cidr=/21 \
                    --enable-ip-alias \
                    --no-enable-autoupgrade \
                    --monitoring=NONE \
                    --logging=NONE \
                    --no-enable-managed-prometheus \
                    --workload-pool=\$GCP_PROJECT.svc.id.goog \
                    --quiet && \
                kubectl create clusterrolebinding cluster-admin-binding --clusterrole cluster-admin --user jenkins@"\$GCP_PROJECT".iam.gserviceaccount.com || ret_val=\$?
                if [ \${ret_val} -eq 0 ]; then break; fi
                ret_num=\$((ret_num + 1))
            done
            if [ \${ret_num} -eq 15 ]; then
                gcloud container clusters list --filter ${CLUSTER_NAME}-${CLUSTER_SUFFIX} --zone ${zone} --format='csv[no-heading](name)' | xargs gcloud container clusters delete --zone ${zone} --quiet || true
                exit 1
            fi
        """
   }
}

void shutdownCluster(String CLUSTER_SUFFIX) {
    withCredentials([string(credentialsId: 'GCP_PROJECT_ID', variable: 'GCP_PROJECT'), file(credentialsId: 'gcloud-key-file', variable: 'CLIENT_SECRET_FILE')]) {
        sh """
            export KUBECONFIG=/tmp/${CLUSTER_NAME}-${CLUSTER_SUFFIX}
            gcloud auth activate-service-account --key-file "\$CLIENT_SECRET_FILE"
            gcloud config set project "\$GCP_PROJECT"
            for namespace in \$(kubectl get namespaces --no-headers | awk '{print \$1}' | grep -vE "^kube-|^openshift" | sed '/-operator/ s/^/1-/' | sort | sed 's/^1-//'); do
                kubectl delete deployments --all -n \$namespace --force --grace-period=0 || true
                kubectl delete sts --all -n \$namespace --force --grace-period=0 || true
                kubectl delete replicasets --all -n \$namespace --force --grace-period=0 || true
                kubectl delete poddisruptionbudget --all -n \$namespace --force --grace-period=0 || true
                kubectl delete services --all -n \$namespace --force --grace-period=0 || true
                kubectl delete pods --all -n \$namespace --force --grace-period=0 || true
            done
            kubectl get svc --all-namespaces || true
            gcloud container clusters delete --zone ${zone} ${CLUSTER_NAME}-${CLUSTER_SUFFIX}
        """
   }
}

void deleteOldClusters(String FILTER) {
    withCredentials([string(credentialsId: 'GCP_PROJECT_ID', variable: 'GCP_PROJECT'), file(credentialsId: 'gcloud-key-file', variable: 'CLIENT_SECRET_FILE')]) {
        sh """
            if gcloud --version > /dev/null 2>&1; then
                gcloud auth activate-service-account --key-file "\$CLIENT_SECRET_FILE"
                gcloud config set project "\$GCP_PROJECT"
                for GKE_CLUSTER in \$(gcloud container clusters list --format='csv[no-heading](name)' --filter="$FILTER"); do
                    GKE_CLUSTER_STATUS=\$(gcloud container clusters list --format='csv[no-heading](status)' --filter="\$GKE_CLUSTER")
                    retry=0
                    while [ "\$GKE_CLUSTER_STATUS" == "PROVISIONING" ]; do
                        echo "Cluster \$GKE_CLUSTER is being provisioned, waiting before delete."
                        sleep 10
                        GKE_CLUSTER_STATUS=\$(gcloud container clusters list --format='csv[no-heading](status)' --filter="\$GKE_CLUSTER")
                        let retry+=1
                        if [ \$retry -ge 60 ]; then
                            echo "Cluster \$GKE_CLUSTER to delete is being provisioned for too long. Skipping..."
                            break
                        fi
                    done
                    gcloud container clusters delete --async --zone ${zone} --quiet \$GKE_CLUSTER || true
                done
            fi
        """
   }
}

void pushLogFile(String FILE_NAME) {
    def LOG_FILE_PATH="e2e-tests/logs/${FILE_NAME}.log"
    def LOG_FILE_NAME="${FILE_NAME}.log"
    echo "Push logfile $LOG_FILE_NAME file to S3!"
    withCredentials([aws(credentialsId: 'AMI/OVF', accessKeyVariable: 'AWS_ACCESS_KEY_ID', secretKeyVariable: 'AWS_SECRET_ACCESS_KEY')]) {
        sh """
            S3_PATH=s3://percona-jenkins-artifactory-public/\$JOB_NAME/${env.GIT_SHORT_COMMIT}
            if [ ! -f ${LOG_FILE_PATH} ]; then
                mkdir -p e2e-tests/logs
                cat > ${LOG_FILE_PATH} <<EOF
Log file ${LOG_FILE_NAME} was not found in Jenkins workspace.
The test may have timed out or terminated before the test runner created/flushed the log.
Build URL: ${BUILD_URL}
EOF
            fi
            aws s3 ls \$S3_PATH/${LOG_FILE_NAME} || :
            aws s3 cp --content-type text/plain --quiet ${LOG_FILE_PATH} \$S3_PATH/${LOG_FILE_NAME}
        """
    }
}

void pushReportFile() {
    echo "Push ${reportHtml} to S3!"
    withCredentials([aws(credentialsId: 'AMI/OVF', accessKeyVariable: 'AWS_ACCESS_KEY_ID', secretKeyVariable: 'AWS_SECRET_ACCESS_KEY')]) {
        sh """
            S3_PATH=s3://percona-jenkins-artifactory-public/\$JOB_NAME/${env.GIT_SHORT_COMMIT}
            aws s3 cp --content-type text/html --quiet ${reportHtml} \$S3_PATH/${reportHtml} || :
        """
    }
}

void pushArtifactFile(String FILE_NAME) {
    echo "Push $FILE_NAME file to S3!"

    withCredentials([aws(credentialsId: 'AMI/OVF', accessKeyVariable: 'AWS_ACCESS_KEY_ID', secretKeyVariable: 'AWS_SECRET_ACCESS_KEY')]) {
        sh """
            touch ${FILE_NAME}
            S3_PATH=s3://percona-jenkins-artifactory/\$JOB_NAME/${env.GIT_SHORT_COMMIT}
            aws s3 ls \$S3_PATH/${FILE_NAME} || :
            aws s3 cp --quiet ${FILE_NAME} \$S3_PATH/${FILE_NAME} || :
        """
    }
}

void initTests() {
    echo "Populating tests into the tests array!"

    def records = readCSV file: 'e2e-tests/run-pr.csv'

    for (int i=0; i<records.size(); i++) {
        tests.add(["name": records[i][0], "cluster": "NA", "result": "skipped", "time": "0"])
    }

    markPassedTests()
}

void markPassedTests() {
    echo "Marking passed tests in the tests map!"

    withCredentials([aws(credentialsId: 'AMI/OVF', accessKeyVariable: 'AWS_ACCESS_KEY_ID', secretKeyVariable: 'AWS_SECRET_ACCESS_KEY')]) {
        sh """
            aws s3 ls "s3://percona-jenkins-artifactory/${JOB_NAME}/${env.GIT_SHORT_COMMIT}/" || :
        """

        def marked = 0
        for (int i=0; i<tests.size(); i++) {
            def testName = tests[i]["name"]
            def file="${env.GIT_BRANCH}-${env.GIT_SHORT_COMMIT}-$testName"
            def retFileExists = sh(
                script: "aws s3api head-object --bucket percona-jenkins-artifactory --key ${JOB_NAME}/${env.GIT_SHORT_COMMIT}/${file} >/dev/null 2>&1",
                returnStatus: true
            )

            if (retFileExists == 0) {
                tests[i]["result"] = "passed"
                marked++
            }
        }
        echo "Marked ${marked}/${tests.size()} test(s) as already passed (will skip on this run)"
    }
}


String formatTime(def time) {
    if (!time || time == "N/A") return "N/A"

    try {
        def totalSeconds = time as Double
        def hours = (totalSeconds / 3600) as Integer
        def minutes = ((totalSeconds % 3600) / 60) as Integer
        def seconds = (totalSeconds % 60) as Integer

        return String.format("%02d:%02d:%02d", hours, minutes, seconds)

    } catch (Exception e) {
        println("Error converting time: ${e.message}")
        return time.toString()
    }
}

@Field def TestsReport = '| Test Name | Result | Time |\r\n| ----------- | -------- | ------ |'

String resultIcon(def result) {
    switch (result) {
        case "passed":  return "✅"
        case "failure": return "❌"
        case "error":   return "⚠️"
        default:        return "⏭️"
    }
}

void makeReport() {
    def wholeTestAmount = tests.size()
    def startedTestAmount = 0
    def failedTestAmount = 0
    def erroredTestAmount = 0
    def totalTestTime = 0

    for (int i=0; i<tests.size(); i++) {
        def testName = tests[i]["name"]
        def testResult = tests[i]["result"]
        def testTime = tests[i]["time"]
        def testUrl = "${testUrlPrefix}/${env.GIT_BRANCH}/${env.GIT_SHORT_COMMIT}/${testName}.log"

        if (testTime instanceof Number) {
            totalTestTime += testTime
        }

        if (testResult != "skipped") {
            startedTestAmount++
        }
        if (testResult == "failure") {
            failedTestAmount++
        }
        if (testResult == "error") {
            erroredTestAmount++
        }
        TestsReport = TestsReport + "\r\n| [" + testName + "](" + testUrl + ") | " + resultIcon(testResult) + " | " + formatTime(testTime) + " |"
    }
    TestsReport = TestsReport + "\r\n\r\n| Summary | Value |\r\n| ------- | ----- |"
    TestsReport = TestsReport + "\r\n| Tests Run | $startedTestAmount/$wholeTestAmount |"
    TestsReport = TestsReport + "\r\n| Tests Failed | $failedTestAmount/$wholeTestAmount  |"
    TestsReport = TestsReport + "\r\n| Tests Errored | $erroredTestAmount/$wholeTestAmount  |"
    TestsReport = TestsReport + "\r\n| Job Duration | " + formatTime(currentBuild.duration / 1000) + " |"
    TestsReport = TestsReport + "\r\n| Total Test Time | "  + formatTime(totalTestTime) + " |"
}

void normalizeReports() {
    sh "mkdir -p e2e-tests/reports"

    for (int i = 0; i < tests.size(); i++) {
        def testName = tests[i]["name"]
        def testResult = tests[i]["result"]
        def testTime = tests[i]["time"] ?: 0

        if (testResult == "skipped") {
            continue
        }

        def xmlFile = "e2e-tests/reports/${testName}.xml"
        def htmlFile = "e2e-tests/reports/${testName}.html"

        // Always collapse to a single testcase per test so python (multi-method) and
        // bash-wrapper tests are counted identically in JUnit. Detail stays in the HTML.
        def failures = testResult == "failure" ? 1 : 0
        def errors = testResult == "error" ? 1 : 0
        def resultElement = ""
        if (testResult == "failure") {
            resultElement = '<failure message="Jenkins reported test failure">Jenkins reported this test as failed. See the HTML report for details.</failure>'
        } else if (testResult == "error") {
            resultElement = '<error message="Jenkins reported test error">Jenkins reported this test as errored (infrastructure/timeout). See the HTML report for details.</error>'
        }

        writeFile file: xmlFile, text: """<?xml version="1.0" encoding="utf-8"?>
<testsuites name="pytest tests">
<testsuite name="psmdb-e2e" errors="${errors}" failures="${failures}" skipped="0" tests="1" time="${testTime}">
<testcase classname="" name="${testName}" time="${testTime}">
${resultElement}
</testcase>
</testsuite>
</testsuites>"""

        if (!fileExists(htmlFile)) {
            def formattedTime = formatTime(testTime)
            def resultCapitalized
            def logMessage
            if (testResult == "failure") {
                resultCapitalized = "Failed"
                logMessage = "Test did not produce a report"
            } else if (testResult == "error") {
                resultCapitalized = "Error"
                logMessage = "Test errored (infrastructure/timeout) and did not produce a report"
            } else {
                resultCapitalized = "Passed"
                logMessage = "Test marked as passed (from previous run)"
            }

            writeFile file: htmlFile, text: """<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8"/>
<title id="head-title">${testName}.html</title>
</head>
<body>
<div id="data-container" data-jsonblob='{"environment": {"Note": "Placeholder report generated because the test report was missing"}, "tests": {"${testName}": [{"extras": [], "result": "${resultCapitalized}", "testId": "${testName}", "duration": "${formattedTime}", "resultsTableRow": ["<td class=\\"col-result\\">${resultCapitalized}</td>", "<td>-</td>", "<td class=\\"col-testId\\">${testName}</td>", "<td class=\\"col-duration\\">${formattedTime}</td>", "<td>-</td>"], "log": "${logMessage}"}]}}'></div>
</body>
</html>"""
        }
    }
}

void formatReportDuration(String htmlFile) {
    def marker = ' tests ran in '
    def suffix = ' seconds'
    def html = readFile(htmlFile)

    def valueStart = html.indexOf(marker)
    if (valueStart < 0) {
        return
    }
    valueStart += marker.length()

    def valueEnd = html.indexOf(suffix, valueStart)
    if (valueEnd < 0) {
        return
    }

    def formatted = formatTime(html.substring(valueStart, valueEnd))
    writeFile file: htmlFile, text: html.substring(0, valueStart) + formatted + html.substring(valueEnd + suffix.length())
}

void clusterRunner(String cluster) {
    withCredentials([aws(credentialsId: 'AMI/OVF', accessKeyVariable: 'AWS_ACCESS_KEY_ID', secretKeyVariable: 'AWS_SECRET_ACCESS_KEY')]) {
        def clusterCreated=0

        for (int i=0; i<tests.size(); i++) {
            if (tests[i]["result"] == "skipped" && currentBuild.nextBuild == null) {
                tests[i]["result"] = "failure"
                tests[i]["cluster"] = cluster
                if (clusterCreated == 0) {
                    createCluster(cluster)
                    clusterCreated++
                }
                runTest(i)
            }
        }

        if (clusterCreated >= 1) {
            shutdownCluster(cluster)
        }
    }
}

void runTest(Integer TEST_ID) {
    def testName = tests[TEST_ID]["name"]
    def clusterSuffix = tests[TEST_ID]["cluster"]
    def timeStart = new Date().getTime()

    try {
        echo "The $testName test was started on cluster ${CLUSTER_NAME}-${clusterSuffix} !"
        tests[TEST_ID]["result"] = "failure"

        timeout(time: 100, unit: 'MINUTES') {
            sh """
                export DEBUG_TESTS=1
                export SKIP_DELETE=0
                export COLUMNS=200
                export KUBECONFIG=/tmp/${CLUSTER_NAME}-${clusterSuffix}
                export GCP_PROJECT=\$GCP_PROJECT
                export GCS_WI_SERVICE_ACCOUNT=percona-psmdb-operator-wi@\$GCP_PROJECT.iam.gserviceaccount.com
                export PATH="\$HOME/.local/bin:\$PATH"
                mkdir -p e2e-tests/logs
                bash -o pipefail <<BASH
                {
                    make e2e-test TEST=${testName}
                } 2>&1 | tee e2e-tests/logs/${testName}.log
BASH
            """
        }
        pushArtifactFile("${env.GIT_BRANCH}-${env.GIT_SHORT_COMMIT}-$testName")
        tests[TEST_ID]["result"] = "passed"
    }
    catch (org.jenkinsci.plugins.workflow.steps.FlowInterruptedException exc) {
        // A per-test timeout is an environment/hang problem, not a test assertion
        // failure: record it as an error and keep the rest of the suite running.
        // Any other interruption (user abort, newer/superseded build) is propagated
        // so the build aborts cleanly.
        def timedOut = exc.causes.any { it.class.name.contains('ExceededTimeout') }
        if (timedOut) {
            echo "Test $testName timed out!"
            tests[TEST_ID]["result"] = "error"
            currentBuild.result = 'FAILURE'
        } else {
            echo "Test $testName was interrupted (build aborted/superseded)!"
            throw exc
        }
    }
    catch (exc) {
        // When a timeout aborts the sh step the shell is killed with SIGTERM and the
        // step can surface as a plain "exit code 143" error before the timeout's
        // FlowInterruptedException propagates. Treat 143 as a timeout/termination
        // (error), not a test assertion failure, so the report shows the right icon.
        if (exc.message?.contains('exit code 143')) {
            echo "Test $testName was terminated (exit 143) - treating as timeout/error!"
            tests[TEST_ID]["result"] = "error"
        } else {
            echo "Test $testName has failed!"
            tests[TEST_ID]["result"] = "failure"
        }
        currentBuild.result = 'FAILURE'
    }
    finally {
        def timeStop = new Date().getTime()
        def durationSec = (timeStop - timeStart) / 1000
        tests[TEST_ID]["time"] = durationSec
        pushLogFile("$testName")
        echo "The $testName test was finished!"
    }
}

void prepareNode() {
    sh """
        sudo curl -sLo /usr/local/bin/kubectl https://dl.k8s.io/release/\$(curl -Ls https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl && sudo chmod +x /usr/local/bin/kubectl
        kubectl version --client --output=yaml

        curl -fsSL https://get.helm.sh/helm-v3.20.0-linux-amd64.tar.gz | sudo tar -C /usr/local/bin --strip-components 1 -xzf - linux-amd64/helm

        sudo curl -fsSL https://github.com/mikefarah/yq/releases/download/v4.48.1/yq_linux_amd64 -o /usr/local/bin/yq && sudo chmod +x /usr/local/bin/yq
        sudo curl -fsSL https://github.com/jqlang/jq/releases/download/jq-1.7.1/jq-linux64 -o /usr/local/bin/jq && sudo chmod +x /usr/local/bin/jq

        sudo tee /etc/yum.repos.d/google-cloud-sdk.repo << EOF
[google-cloud-cli]
name=Google Cloud CLI
baseurl=https://packages.cloud.google.com/yum/repos/cloud-sdk-el9-x86_64
enabled=1
gpgcheck=1
repo_gpgcheck=0
gpgkey=https://packages.cloud.google.com/yum/doc/rpm-package-key.gpg
EOF
        sudo yum install -y google-cloud-cli google-cloud-cli-gke-gcloud-auth-plugin

        curl -sL https://github.com/mitchellh/golicense/releases/latest/download/golicense_0.2.0_linux_x86_64.tar.gz | sudo tar -C /usr/local/bin -xzf - golicense

        curl -LsSf https://astral.sh/uv/install.sh | sh
        export PATH="\$HOME/.local/bin:\$PATH"
        uv sync --locked
    """
    installAzureCLI()
    azureAuth()
}

void azureAuth() {
    withCredentials([azureServicePrincipal('PERCONA-OPERATORS-SP')]) {
        sh '''
            az login --service-principal -u "$AZURE_CLIENT_ID" -p "$AZURE_CLIENT_SECRET" -t "$AZURE_TENANT_ID"  --allow-no-subscriptions
            az account set -s "$AZURE_SUBSCRIPTION_ID"
        '''
    }
}

void installAzureCLI() {
    sh """
        if ! command -v az &>/dev/null; then
            echo "Installing Azure CLI for Hetzner instances..."
            sudo rpm --import https://packages.microsoft.com/keys/microsoft.asc
            cat <<EOF | sudo tee /etc/yum.repos.d/azure-cli.repo
[azure-cli]
name=Azure CLI
baseurl=https://packages.microsoft.com/yumrepos/azure-cli
enabled=1
gpgcheck=1
gpgkey=https://packages.microsoft.com/keys/microsoft.asc
EOF
            sudo dnf install azure-cli -y
        fi
    """
}

boolean isManualBuild() {
    def causes = currentBuild.getBuildCauses('hudson.model.Cause$UserIdCause')
    return !causes.isEmpty()
}

@Field def needToRunTests = true
void checkE2EIgnoreFiles() {
    if (isManualBuild()) {
        echo "This is a manual rebuild. Forcing pipeline execution."
        return
    }

    def e2eignoreFile = ".e2eignore"
    if ( ! fileExists(e2eignoreFile) ) {
        echo "No $e2eignoreFile file found. Proceeding with execution."
        return
    }

    def excludedFiles = readFile(e2eignoreFile).split('\n').collect{it.trim()}
    def lastProcessedCommitFile = "last-processed-commit.txt"
    def lastProcessedCommitHash = ""

    def build = currentBuild.previousBuild
    while (build != null) {
        try {
            echo "Checking previous build: #$build.number"
            copyArtifacts(projectName: env.JOB_NAME, selector: specific("$build.number"), filter: lastProcessedCommitFile)
            lastProcessedCommitHash = readFile(lastProcessedCommitFile).trim()
            echo "Last processed commit hash: $lastProcessedCommitHash"
            break
        } catch (Exception e) {
            echo "No $lastProcessedCommitFile found in build $build.number. Checking earlier builds."
        }
        build = build.previousBuild
    }

    if (lastProcessedCommitHash == "") {
        echo "This is the first run. Using merge base as the starting point for the diff."
        changedFiles = sh(script: "git diff --name-only \$(git merge-base HEAD origin/$CHANGE_TARGET)", returnStdout: true).trim().split('\n').findAll{it}
    } else {
        def commitExists = sh(script: "git cat-file -e $lastProcessedCommitHash 2>/dev/null", returnStatus: true) == 0
        if (commitExists) {
            echo "Processing changes since last processed commit: $lastProcessedCommitHash"
            changedFiles = sh(script: "git diff --name-only $lastProcessedCommitHash HEAD", returnStdout: true).trim().split('\n').findAll{it}
        } else {
            echo "Commit hash $lastProcessedCommitHash does not exist in the current repository. Using merge base as the starting point for the diff."
            changedFiles = sh(script: "git diff --name-only \$(git merge-base HEAD origin/$CHANGE_TARGET)", returnStdout: true).trim().split('\n').findAll{it}
        }
    }

    echo "Excluded files: $excludedFiles"
    echo "Changed files: $changedFiles"

    // Use placeholder so the * in ".*" (from **) is not replaced by [^/]*
    def excludedFilesRegex = excludedFiles.collect{
        it.replace("**", ".__STARSTAR__").replace("*", "[^/]*").replace(".__STARSTAR__", ".*")
    }
    needToRunTests = !changedFiles.every{changed -> excludedFilesRegex.any{regex -> changed ==~ regex}}

    if (needToRunTests) {
        echo "Some changed files are outside of the e2eignore list. Proceeding with execution."
    } else {
        if (currentBuild.previousBuild?.result != 'SUCCESS' && currentBuild.number != 1) {
            echo "All changed files are e2eignore files, and previous build was unsuccessful. Propagating previous state."
            currentBuild.result = currentBuild.previousBuild?.result
            error "Skipping execution as non-significant changes detected and previous build was unsuccessful."
        } else {
            echo "All changed files are e2eignore files. Aborting pipeline execution."
        }
    }

    sh """
        echo \$(git rev-parse HEAD) > $lastProcessedCommitFile
    """
    archiveArtifacts "$lastProcessedCommitFile"
}

def isPRJob = false
if (env.CHANGE_URL) {
    isPRJob = true
}

pipeline {
    environment {
        CLOUDSDK_CORE_DISABLE_PROMPTS = 1
        CLEAN_NAMESPACE = 1
        OPERATOR_NS = 'psmdb-operator'
        GIT_SHORT_COMMIT = sh(script: 'git rev-parse --short HEAD', returnStdout: true).trim()
        VERSION = "${env.GIT_BRANCH}-${env.GIT_SHORT_COMMIT}"
        CLUSTER_NAME = sh(script: "echo jen-psmdb-${env.CHANGE_ID}-${GIT_SHORT_COMMIT}-${env.BUILD_NUMBER} | tr '[:upper:]' '[:lower:]'", returnStdout: true).trim()
        AUTHOR_NAME = sh(script: "echo ${CHANGE_AUTHOR_EMAIL} | awk -F'@' '{print \$1}'", returnStdout: true).trim()
    }
    agent {
        label 'docker-x64-min'
    }
    options {
        disableConcurrentBuilds(abortPrevious: true)
        copyArtifactPermission("$JOB_NAME/PR-*")
    }
    stages {
        stage('Check Ignore Files') {
            when {
                expression {
                    isPRJob
                }
            }
            steps {
                checkE2EIgnoreFiles()
            }
        }
        stage('Prepare') {
            when {
                expression {
                    isPRJob && needToRunTests
                }
            }
            steps {
                initTests()
                prepareNode()
                script {
                    if (AUTHOR_NAME == 'null') {
                        AUTHOR_NAME = sh(script: "git show -s --pretty=%ae | awk -F'@' '{print \$1}'", returnStdout: true).trim()
                    }
                    for (comment in pullRequest.comments) {
                        println("Author: ${comment.user}, Comment: ${comment.body}")
                        if (comment.user.equals('JNKPercona')) {
                            println("delete comment")
                            comment.delete()
                        }
                    }
                }
                withCredentials([file(credentialsId: 'cloud-secret-file-psmdb', variable: 'CLOUD_SECRET_FILE')]) {
                    sh '''
                        cp $CLOUD_SECRET_FILE e2e-tests/conf/cloud-secret.yml
                    '''
                }
                deleteOldClusters("jen-psmdb-$CHANGE_ID")
            }
        }
        stage('Build docker image') {
            when {
                expression {
                    isPRJob && needToRunTests
                }
            }
            steps {
                withCredentials([usernamePassword(credentialsId: 'hub.docker.com', passwordVariable: 'PASS', usernameVariable: 'USER')]) {
                    sh '''
                        DOCKER_TAG=perconalab/percona-server-mongodb-operator:$VERSION
                        docker_tag_file='./results/docker/TAG'
                        mkdir -p $(dirname ${docker_tag_file})
                        echo ${DOCKER_TAG} > "${docker_tag_file}"
                            sg docker -c "
                                echo '\$PASS' | docker login -u '\$USER' --password-stdin
                                export RELEASE=0
                                export IMAGE=\$DOCKER_TAG
                                DOCKER_DEFAULT_PLATFORM=linux/amd64,linux/arm64 ./e2e-tests/build
                                docker logout
                            "
                        sudo rm -rf ./build
                    '''
                }
                stash includes: 'results/docker/TAG', name: 'IMAGE'
                archiveArtifacts 'results/docker/TAG'
            }
        }
        stage('GoLicenseDetector test') {
            when {
                expression {
                    isPRJob && needToRunTests
                }
            }
            steps {
                sh """
                    mkdir -p $WORKSPACE/src/github.com/percona
                    ln -s $WORKSPACE $WORKSPACE/src/github.com/percona/percona-server-mongodb-operator
                    sg docker -c "
                        docker run \
                            --rm \
                            -v $WORKSPACE/src/github.com/percona/percona-server-mongodb-operator:/go/src/github.com/percona/percona-server-mongodb-operator \
                            -w /go/src/github.com/percona/percona-server-mongodb-operator \
                            -e GOFLAGS='-buildvcs=false' \
                            golang:1.26 sh -c '
                                go install github.com/google/go-licenses@v1.6.0;
                                /go/bin/go-licenses csv github.com/percona/percona-server-mongodb-operator/cmd/manager \
                                    | cut -d , -f 3 \
                                    | sort -u \
                                    > go-licenses-new || :
                            '
                    "
                    diff -u e2e-tests/license/compare/go-licenses go-licenses-new
                """
            }
        }
        stage('GoLicense test') {
            when {
                expression {
                    isPRJob && needToRunTests
                }
            }
            steps {
                sh '''
                    mkdir -p $WORKSPACE/src/github.com/percona
                    ln -s $WORKSPACE $WORKSPACE/src/github.com/percona/percona-server-mongodb-operator
                    sg docker -c "
                        docker run \
                            --rm \
                            -v $WORKSPACE/src/github.com/percona/percona-server-mongodb-operator:/go/src/github.com/percona/percona-server-mongodb-operator \
                            -w /go/src/github.com/percona/percona-server-mongodb-operator \
                            -e GOFLAGS='-buildvcs=false' \
                            golang:1.26 sh -c 'go build -v -o percona-server-mongodb-operator github.com/percona/percona-server-mongodb-operator/cmd/manager'
                    "
                '''

                withCredentials([string(credentialsId: 'GITHUB_API_TOKEN', variable: 'GITHUB_TOKEN')]) {
                    sh """
                        golicense -plain ./percona-server-mongodb-operator \
                            | grep -v 'license not found' \
                            | sed -r 's/^[^ ]+[ ]+//' \
                            | sort \
                            | uniq \
                            > golicense-new || true
                        diff -u e2e-tests/license/compare/golicense golicense-new
                    """
                }
            }
        }
        stage('Run tests for operator') {
            when {
                expression {
                    isPRJob && needToRunTests
                }
            }
            options {
                timeout(time: 5, unit: 'HOURS')
            }
            steps {
                script {
                    def branches = [:]
                    for (int i = 1; i <= clusterCount; i++) {
                        def cluster = "cluster${i}".toString()
                        branches[cluster] = {
                            stage(cluster) {
                                clusterRunner(cluster)
                            }
                        }
                    }
                    parallel branches
                }
            }
        }
    }
    post {
        always {
            script {
                echo "CLUSTER ASSIGNMENTS\n" + tests.toString().replace("], ","]\n").replace("]]","]").replaceFirst("\\[","")

                if (currentBuild.result != null && currentBuild.result != 'SUCCESS' && currentBuild.nextBuild == null) {
                    try {
                        slackSend channel: "@${AUTHOR_NAME}", color: '#FF0000', message: "[${JOB_NAME}]: build ${currentBuild.result}, ${BUILD_URL} owner: @${AUTHOR_NAME}"
                    }
                    catch (exc) {
                        slackSend channel: '#cloud-dev-ci', color: '#FF0000', message: "[${JOB_NAME}]: build ${currentBuild.result}, ${BUILD_URL} owner: @${AUTHOR_NAME}"
                    }
                }
                if (needToRunTests) {
                    if (isPRJob && currentBuild.nextBuild == null) {
                        for (comment in pullRequest.comments) {
                            println("Author: ${comment.user}, Comment: ${comment.body}")
                            if (comment.user.equals('JNKPercona')) {
                                println("delete comment")
                                comment.delete()
                            }
                        }
                        makeReport()
                        normalizeReports()
                        
                        sh """
                            export PATH="\$HOME/.local/bin:\$PATH"
                            uv run pytest_html_merger -i e2e-tests/reports -o ${reportHtml} -t "PSMDB e2e tests - ${env.GIT_BRANCH} (${env.GIT_SHORT_COMMIT})"
                            uv run junitparser merge --glob 'e2e-tests/reports/*.xml' ${reportXml}
                        """
                        formatReportDuration(reportHtml)
                        junit testResults: reportXml, healthScaleFactor: 1.0
                        archiveArtifacts "${reportXml}, ${reportHtml}"
                        pushReportFile()

                        unstash 'IMAGE'
                        def IMAGE = sh(returnStdout: true, script: "cat results/docker/TAG").trim()
                        TestsReport = TestsReport + "\r\n\r\nCommit: ${env.CHANGE_URL}/commits/${env.GIT_COMMIT}\r\nImage: `${IMAGE}`\r\nTest report: [report](${testUrlPrefix}/${env.GIT_BRANCH}/${env.GIT_SHORT_COMMIT}/${reportHtml})\r\n"
                        pullRequest.comment(TestsReport)
                    }
                    deleteOldClusters("$CLUSTER_NAME")
                    sh """
                        sudo docker system prune --volumes -af
                    """
                }
                deleteDir()
            }
        }
    }
}
