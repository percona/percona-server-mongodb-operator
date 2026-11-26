import groovy.transform.Field

@Field def zone = 'us-central1-a'
@Field def testUrlPrefix = 'https://percona-jenkins-artifactory-public.s3.amazonaws.com/cloud-psmdb-operator'
@Field def tests = []
@Field def reportHtml = 'e2e-test-report.html'
@Field def reportXml = 'e2e-test-report.xml'
@Field int clusterCount = 15
// Tests that run at the same time on each cluster. 1 runs one test at a time per cluster.
@Field int testsPerCluster = 3
// Cluster load with several tests per cluster: `kubectl top nodes` every loadIntervalSeconds. A
// cluster counts as highly loaded when its busiest node stays at loadCpuPercent CPU or
// loadMemoryPercent memory for loadHighAfterSeconds without a break.
@Field int loadIntervalSeconds = 30
@Field int loadCpuPercent = 90
@Field int loadMemoryPercent = 70
@Field int loadHighAfterSeconds = 120
@Field def clusterLoadLines = [:]

void createCluster(String CLUSTER_SUFFIX) {
    withCredentials([string(credentialsId: 'GCP_PROJECT_ID', variable: 'GCP_PROJECT'), file(credentialsId: 'gcloud-key-file', variable: 'CLIENT_SECRET_FILE')]) {
        sh """
            export GKE_CLUSTER_NAME=${CLUSTER_NAME}-${CLUSTER_SUFFIX}
            export KUBECONFIG=/tmp/\$GKE_CLUSTER_NAME
            export CLOUDSDK_CONFIG=/tmp/gcloud-\$GKE_CLUSTER_NAME
            gcloud auth activate-service-account --key-file "\$CLIENT_SECRET_FILE"
            gcloud config set project "\$GCP_PROJECT"
            GKE_VERSION=\$(gcloud container get-server-config --zone ${zone} --flatten='channels[].validVersions[]' --filter='channels.channel=STABLE' --format='value(channels.validVersions)' | sort -V | head -n1)
            if [ -z "\${GKE_VERSION}" ]; then
                echo "Failed to detect the minimum Kubernetes version from the GKE stable release channel"
                exit 1
            fi

            cluster_exists() {
                gcloud container clusters describe "\${GKE_CLUSTER_NAME}" --zone ${zone} >/dev/null 2>&1
            }

            delete_cluster() {
                for attempt in 1 2; do
                    if gcloud container clusters delete "\${GKE_CLUSTER_NAME}" --zone ${zone} --quiet; then
                        return 0
                    fi
                    if ! cluster_exists; then
                        return 0
                    fi
                    if [ \${attempt} -eq 1 ]; then
                        echo "Failed to delete GKE cluster \${GKE_CLUSTER_NAME}; retrying in 10 seconds"
                        sleep 10
                    fi
                done
                return 1
            }

            ret_num=0
            while [ \${ret_num} -lt 15 ]; do
                ret_val=0
                if cluster_exists; then
                    delete_cluster || echo "Unable to remove existing GKE cluster \${GKE_CLUSTER_NAME}; continuing with cluster creation"
                fi
                echo "Creating GKE cluster \${GKE_CLUSTER_NAME} with Kubernetes version \${GKE_VERSION} from the stable release channel"
                gcloud container clusters create "\${GKE_CLUSTER_NAME}" \
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
                if cluster_exists; then
                    delete_cluster || true
                fi
                exit 1
            fi
        """
   }
}

void shutdownCluster(String CLUSTER_SUFFIX) {
    withCredentials([string(credentialsId: 'GCP_PROJECT_ID', variable: 'GCP_PROJECT'), file(credentialsId: 'gcloud-key-file', variable: 'CLIENT_SECRET_FILE')]) {
        sh """
            export GKE_CLUSTER_NAME=${CLUSTER_NAME}-${CLUSTER_SUFFIX}
            export KUBECONFIG=/tmp/\$GKE_CLUSTER_NAME
            export CLOUDSDK_CONFIG=/tmp/gcloud-\$GKE_CLUSTER_NAME
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
            gcloud container clusters delete --zone ${zone} "\$GKE_CLUSTER_NAME"
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
                    GKE_CLUSTER_STATUS=\$(gcloud container clusters list --format='csv[no-heading](status)' --filter="name=\$GKE_CLUSTER")
                    retry=0
                    while [ "\$GKE_CLUSTER_STATUS" == "PROVISIONING" ]; do
                        echo "Cluster \$GKE_CLUSTER is being provisioned, waiting before delete."
                        sleep 10
                        GKE_CLUSTER_STATUS=\$(gcloud container clusters list --format='csv[no-heading](status)' --filter="name=\$GKE_CLUSTER")
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

// Publishes the Allure report of the tests run in this build. It is kept with the build in
// Jenkins (not uploaded to S3) and removed by the build retention. A publishing problem
// does not fail the build. Returns true when the report was published.
boolean publishAllureReport() {
    if (sh(returnStatus: true, script: 'ls e2e-tests/allure-results/*-result.json >/dev/null 2>&1') != 0) {
        echo 'No Allure results in e2e-tests/allure-results, skipping the Allure report'
        return false
    }
    try {
        allure includeProperties: false, results: [[path: 'e2e-tests/allure-results']]
        return true
    } catch (org.jenkinsci.plugins.workflow.steps.FlowInterruptedException exc) {
        throw exc
    } catch (Throwable exc) {
        // Throwable: a missing plugin raises NoSuchMethodError, which is not an Exception.
        echo "Warning: failed to publish the Allure report: ${exc}"
        return false
    }
}

// The Allure timeline groups tests by their host and thread labels. All slots run on one Jenkins
// agent, so pytest gives every test the same host. This sets host to the cluster and thread to the
// slot, adds the cluster load while each test case ran, and moves the results into
// e2e-tests/allure-results. A problem here does not fail the build.
void finishSlotAllureResults(String resultsDir, String cluster, String slot) {
    try {
        withEnv([
            "RESULTS=${resultsDir}",
            "LABEL_HOST=${CLUSTER_NAME}-${cluster}",
            "LABEL_THREAD=${slot}",
            "LOAD_SAMPLES=${clusterLoadSamples(cluster)}",
            "CPU_LIMIT=${loadCpuPercent}",
            "MEM_LIMIT=${loadMemoryPercent}",
            "INTERVAL=${loadIntervalSeconds}"
        ]) {
            sh '''
                [ -d "$RESULTS" ] || exit 0
                for result in "$RESULTS"/*-result.json; do
                    [ -f "$result" ] || continue
                    jq --arg host "$LABEL_HOST" --arg thread "$LABEL_THREAD" '
                        .labels = ([(.labels // [])[] | select(.name != "host" and .name != "thread")]
                            + [{"name": "host", "value": $host}, {"name": "thread", "value": $thread}])
                    ' "$result" > "$result.tmp" && mv "$result.tmp" "$result"

                    [ -s "$LOAD_SAMPLES" ] || continue
                    # The window starts one interval before the test case, so a short case still gets a sample.
                    start=$(jq -r '((.start // 0) / 1000 | floor)' "$result")
                    stop=$(jq -r '((.stop // 0) / 1000 | ceil)' "$result")
                    from=$((start - INTERVAL))
                    summary=$(awk -v cpu_limit="$CPU_LIMIT" -v mem_limit="$MEM_LIMIT" -v interval="$INTERVAL" \
                        -v from="$from" -v to="$stop" -f "$LOAD_SAMPLES.awk" "$LOAD_SAMPLES")
                    get() { printf '%s\\n' "$summary" | sed -n "s/^$1=//p"; }
                    [ "$(get samples)" != 0 ] || continue

                    value="busiest node CPU $(get cpu_peak)%, memory $(get mem_peak)%"
                    [ "$(get cpu_over)" = 0 ] || value="$value; CPU ${CPU_LIMIT}%+ for $(get cpu_over)s"
                    [ "$(get mem_over)" = 0 ] || value="$value; memory ${MEM_LIMIT}%+ for $(get mem_over)s"

                    attachment="$(basename "$result" -result.json)-cluster-load-attachment.txt"
                    {
                        echo "Cluster load while the test case ran ($value)."
                        echo "Samples every ${INTERVAL}s from kubectl top nodes, seconds from the test case start."
                        echo
                        echo "second node cpu cpu% memory memory%"
                        awk -v from="$from" -v to="$stop" -v start="$start" \
                            '$1 >= from && $1 <= to { $1 = $1 - start; print }' "$LOAD_SAMPLES"
                    } > "$RESULTS/$attachment"

                    # excluded: the value differs between attempts, and Allure groups a retry with its
                    # earlier attempts only when their parameters match.
                    jq --arg value "$value" --arg source "$attachment" '
                        .parameters = ((.parameters // []) + [{"name": "Cluster load", "value": $value, "excluded": true}])
                        | .attachments = ((.attachments // []) + [{"name": "Cluster load", "source": $source, "type": "text/plain"}])
                    ' "$result" > "$result.tmp" && mv "$result.tmp" "$result"
                done
                mkdir -p e2e-tests/allure-results
                find "$RESULTS" -mindepth 1 -maxdepth 1 -exec mv -f {} e2e-tests/allure-results/ \\;
                rm -rf "$RESULTS"
            '''
        }
    } catch (org.jenkinsci.plugins.workflow.steps.FlowInterruptedException exc) {
        throw exc
    } catch (allureErr) {
        echo "Warning: failed to label the Allure results in ${resultsDir}: ${allureErr}"
    }
}

String clusterLoadSamples(String cluster) {
    return "${env.WORKSPACE}/cluster-load-${CLUSTER_NAME}-${cluster}.samples"
}

// Appends `kubectl top nodes` of the cluster to its samples file every loadIntervalSeconds, in a
// background loop on the agent: <epoch> <node> <cpu cores> <cpu%> <memory> <memory%>. The loop
// stops when finishClusterLoad deletes the pid file or the workspace is deleted. GKE has
// metrics-server by default; without it the file stays empty.
void startClusterLoad(String cluster) {
    def samples = clusterLoadSamples(cluster)
    try {
        // Written once here: the slots of the cluster read it at the same time.
        writeFile file: "${samples}.awk", text: clusterLoadAwk()
        withEnv([
            "KUBECONFIG=/tmp/${CLUSTER_NAME}-${cluster}",
            "LOAD_SAMPLES=${samples}",
            "LOAD_INTERVAL=${loadIntervalSeconds}"
        ]) {
            // JENKINS_NODE_COOKIE and BUILD_ID keep Jenkins from killing the loop when this step ends.
            sh '''
                rm -f "$LOAD_SAMPLES" "$LOAD_SAMPLES.pid"
                touch "$LOAD_SAMPLES"
                JENKINS_NODE_COOKIE=dontKillMe BUILD_ID=dontKillMe nohup bash -c '
                    while [ -e "$LOAD_SAMPLES" ]; do
                        now=$(date +%s)
                        kubectl top nodes --no-headers --request-timeout=20s 2>/dev/null \
                            | sed "s/^/$now /" >> "$LOAD_SAMPLES"
                        sleep "$LOAD_INTERVAL"
                    done
                ' </dev/null >/dev/null 2>&1 &
                echo $! > "$LOAD_SAMPLES.pid"
            '''
        }
    } catch (org.jenkinsci.plugins.workflow.steps.FlowInterruptedException exc) {
        throw exc
    } catch (loadErr) {
        echo "Warning: cluster load sampling did not start on ${CLUSTER_NAME}-${cluster}: ${loadErr}"
    }
}

// Stops sampling and prints one line with the cluster's load, which writeClusterLoadEnvironment
// also adds to the Allure overview. Never fails the build.
void finishClusterLoad(String cluster) {
    def samples = clusterLoadSamples(cluster)
    try {
        def output = sh(returnStdout: true, script: """
            if [ -f '${samples}.pid' ]; then
                kill "\$(cat '${samples}.pid')" 2>/dev/null || true
                rm -f '${samples}.pid'
            fi
            if [ ! -s '${samples}' ]; then echo samples=0; exit 0; fi
            awk -v cpu_limit=${loadCpuPercent} -v mem_limit=${loadMemoryPercent} \
                -v interval=${loadIntervalSeconds} -v from=0 -v to=0 \
                -f '${samples}.awk' '${samples}'
        """).trim()
        def s = [samples: '0']
        for (line in output.split('\n')) {
            def parts = line.split('=', 2)
            if (parts.size() == 2) {
                s[parts[0]] = parts[1]
            }
        }
        if (s.samples == '0') {
            echo "Cluster load on ${CLUSTER_NAME}-${cluster}: no samples (is metrics-server running?)"
            return
        }
        def high = (s.cpu_longest as Integer) >= loadHighAfterSeconds || (s.mem_longest as Integer) >= loadHighAfterSeconds
        def summary = "${high ? 'HIGH ' : ''}busiest node CPU peak ${s.cpu_peak}% " +
            "(${s.cpu_over}s at ${loadCpuPercent}%+), memory peak ${s.mem_peak}% " +
            "(${s.mem_over}s at ${loadMemoryPercent}%+), cluster average CPU peak ${s.cluster_cpu_peak}%"
        echo "Cluster load on ${CLUSTER_NAME}-${cluster}: ${summary}"
        recordClusterLoad(cluster, summary)
    } catch (org.jenkinsci.plugins.workflow.steps.FlowInterruptedException exc) {
        throw exc
    } catch (loadErr) {
        echo "Warning: cluster load report failed on ${CLUSTER_NAME}-${cluster}: ${loadErr}"
    }
}

@NonCPS
void recordClusterLoad(String cluster, String summary) {
    synchronized (clusterLoadLines) {
        clusterLoadLines[cluster] = summary
    }
}

// One Environment entry per cluster on the Allure overview. Tests write environment.properties
// into their results, so this runs after they all finished.
void writeClusterLoadEnvironment() {
    if (!clusterLoadLines || !fileExists('e2e-tests/allure-results')) {
        return
    }
    def lines = ["Tests.per.cluster=${testsPerCluster}"]
    for (cluster in clusterLoadLines.keySet().sort()) {
        lines << "Cluster.load.${cluster}=${clusterLoadLines[cluster]}"
    }
    def file = 'e2e-tests/allure-results/environment.properties'
    def existing = fileExists(file) ? readFile(file).trim() : ''
    writeFile file: file, text: (existing ? existing + '\n' : '') + lines.join('\n') + '\n'
}

// Reduces the samples to one line per timestamp (the busiest node) and prints key=value lines.
// Time above a limit counts whole sample intervals; *_longest is the longest unbroken stretch.
String clusterLoadAwk() {
    return '''
BEGIN { cpu_peak = -1; mem_peak = -1; cluster_peak = -1 }
function flush() {
    if (ts == "") return
    n++
    if (maxc >= cpu_limit) { cpu_over += interval; cpu_run += interval } else cpu_run = 0
    if (cpu_run > cpu_longest) cpu_longest = cpu_run
    if (maxm >= mem_limit) { mem_over += interval; mem_run += interval } else mem_run = 0
    if (mem_run > mem_longest) mem_longest = mem_run
    mean = sumc / cnt
    mean_total += mean
    if (mean > cluster_peak) cluster_peak = mean
    if (maxc > cpu_peak) { cpu_peak = maxc; cpu_node = maxcn; cpu_at = ts }
    if (maxm > mem_peak) { mem_peak = maxm; mem_node = maxmn; mem_at = ts }
}
$4 ~ /%$/ && $6 ~ /%$/ && $1 >= from && (to == 0 || $1 <= to) {
    if ($1 != ts) { flush(); ts = $1; maxc = -1; maxm = -1; sumc = 0; cnt = 0 }
    c = $4 + 0
    m = $6 + 0
    if (c > maxc) { maxc = c; maxcn = $2 }
    if (m > maxm) { maxm = m; maxmn = $2 }
    sumc += c
    cnt++
}
END {
    flush()
    if (n == 0) { print "samples=0"; exit }
    printf "samples=%d\\n", n
    printf "cpu_peak=%d\\ncpu_node=%s\\ncpu_at=%s\\n", cpu_peak, cpu_node, cpu_at
    printf "cpu_over=%d\\ncpu_longest=%d\\n", cpu_over, cpu_longest
    printf "mem_peak=%d\\nmem_node=%s\\nmem_at=%s\\n", mem_peak, mem_node, mem_at
    printf "mem_over=%d\\nmem_longest=%d\\n", mem_over, mem_longest
    printf "cluster_cpu_peak=%d\\ncluster_cpu_mean=%d\\n", cluster_peak, mean_total / n
}
'''
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

String detectMongoVersion() {
    return sh(
        script: '''
            set -eu
            mongo_image=${IMAGE_MONGOD:-}
            if [ -z "$mongo_image" ]; then
                mongo_image=$(sed -nE 's#.*"IMAGE_MONGOD": "([^"]+)".*#\\1#p' e2e-tests/conftest.py | head -n1)
                if [ -z "$mongo_image" ]; then
                    echo "IMAGE_MONGOD default not found in e2e-tests/conftest.py" >&2
                    exit 1
                fi
            fi

            mongo_version=$(printf '%s\n' "$mongo_image" | sed -nE 's#.*(:main-mongod|:)([0-9]+[.][0-9]+)([.-].*)?$#\\2#p')
            if [ -z "$mongo_version" ]; then
                echo "Unable to detect MongoDB version from image: $mongo_image" >&2
                exit 1
            fi

            printf '%s' "$mongo_version"
        ''',
        returnStdout: true
    ).trim()
}

void initTests() {
    echo "Populating tests into the tests array!"
    def mongoVersion = detectMongoVersion()
    echo "Detected MongoDB version: ${mongoVersion}"

    def output = sh(
        script: "export PATH=\"\$HOME/.local/bin:\$PATH\"; uv run e2e-tests/select_tests.py list --suite pr --platform gke --operator-mode cluster-wide --mongo-version ${mongoVersion} --format lines",
        returnStdout: true
    ).trim()
    def records = output.split('\n').findAll { it }

    for (int i=0; i<records.size(); i++) {
        tests.add(["name": records[i], "cluster": "NA", "result": "skipped", "time": "0"])
    }

    if (testsPerCluster > 1) {
        loadTestPlacement()
    }
    markPassedTests()
}

// Reads the placement tags from e2e-tests/tests.yaml: an ownCluster test runs alone on a cluster,
// and tests with the same lock never run at the same time on one cluster.
void loadTestPlacement() {
    // One line per tagged test: "<name> <own|shared> <lock,lock|->".
    def output = sh(
        script: '''
            export PATH="$HOME/.local/bin:$PATH"
            uv run e2e-tests/select_tests.py placement | tail -n 1 \
                | jq -r 'to_entries[] | [.key, (if .value.ownCluster then "own" else "shared" end), ((.value.locks // []) | if length == 0 then "-" else join(",") end)] | join(" ")'
        ''',
        returnStdout: true
    ).trim()

    def placement = [:]
    for (line in output.split('\n')) {
        def fields = line.trim().split(' ')
        if (fields.size() == 3) {
            placement[fields[0]] = [own: fields[1] == 'own', locks: fields[2] == '-' ? [] : fields[2].split(',').toList()]
        }
    }
    def own = 0
    def locked = 0
    for (test in tests) {
        def entry = placement[test.name]
        test.ownCluster = entry ? entry.own : false
        test.locks = entry ? entry.locks : []
        own += test.ownCluster ? 1 : 0
        locked += test.locks ? 1 : 0
    }
    echo "Placement: ${own} tests run alone on a cluster, ${locked} tests have locks"
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

boolean isClusterReachable(String clusterSuffix) {
    return sh(returnStatus: true, script: """
        export GKE_CLUSTER_NAME=${CLUSTER_NAME}-${clusterSuffix}
        export KUBECONFIG=/tmp/\$GKE_CLUSTER_NAME
        for attempt in 1 2; do
            if kubectl get nodes --request-timeout=60s; then
                exit 0
            fi
            if [ \${attempt} -eq 1 ]; then
                echo "Cluster \$GKE_CLUSTER_NAME is unreachable; retrying in 10 seconds"
                sleep 10
            fi
        done
        exit 1
    """) == 0
}

void clusterRunner(String cluster) {
    withCredentials([aws(credentialsId: 'AMI/OVF', accessKeyVariable: 'AWS_ACCESS_KEY_ID', secretKeyVariable: 'AWS_SECRET_ACCESS_KEY')]) {
        if (testsPerCluster > 1) {
            if (countSkippedTests() > 0 && currentBuild.nextBuild == null) {
                createCluster(cluster)
                startClusterLoad(cluster)
                try {
                    runTestSlots(cluster)
                } finally {
                    finishClusterLoad(cluster)
                }
                shutdownCluster(cluster)
            }
            return
        }

        def clusterCreated=0

        for (int i=0; i<tests.size(); i++) {
            if (tests[i]["result"] == "skipped" && currentBuild.nextBuild == null) {
                tests[i]["result"] = "failure"
                tests[i]["cluster"] = cluster
                if (clusterCreated == 0) {
                    createCluster(cluster)
                    clusterCreated++
                } else if (!isClusterReachable(cluster)) {
                    echo "Cluster ${CLUSTER_NAME}-${cluster} is unreachable, recreating it"
                    createCluster(cluster)
                }
                runTest(i)
            }
        }

        if (clusterCreated >= 1) {
            shutdownCluster(cluster)
        }
    }
}

// Runs up to testsPerCluster tests at the same time on one cluster. Each slot takes the next test it
// may start (claimNextTest), so a slot that finishes a short test starts the next one at once.
void runTestSlots(String cluster) {
    echo "Running up to ${testsPerCluster} tests at a time on cluster ${CLUSTER_NAME}-${cluster}"
    def branches = [:]
    for (int i = 1; i <= testsPerCluster; i++) {
        def slot = "${cluster}-slot${i}".toString()
        branches[slot] = {
            while (currentBuild.nextBuild == null) {
                def testId = claimNextTest(cluster)
                if (testId == null) {
                    break
                }
                if (testId < 0) {
                    // Tests are left, but an ownCluster test or a lock keeps this slot waiting.
                    sleep(time: 30, unit: 'SECONDS')
                    continue
                }
                try {
                    runSlotTest(testId, slot)
                } finally {
                    markTestFinished(testId)
                }
            }
        }
    }
    parallel branches
}

// One test in a slot. It gets its own kubeconfig and helm home, and must not delete what the
// tests next to it use:
// - DELETE_CRD_ON_START=0: the CRD stays.
// - CLEAN_NAMESPACE=0: with 1, a starting test deletes every namespace on the cluster.
// - SKIP_DELETE=1: the teardown would also remove cert-manager and the RBAC. cleanupSlotTest
//   deletes only this test's namespaces instead.
// - Cluster-wide: a test that shares the cluster gets its own operator namespace
//   <test>-<hex>-op, and SHARED_CLUSTER=1 makes that operator use its own ClusterRole and
//   ClusterRoleBinding and watch only the test namespace.
// A test that fails next to other tests is retried once, alone on a cluster, so a failure caused
// by sharing does not fail the build, and "passed alone" shows which tests need ownCluster.
void runSlotTest(Integer testId, String slot) {
    def test = tests[testId]
    def testName = test.name
    def clusterKubeconfig = "/tmp/${CLUSTER_NAME}-${test.cluster}"
    def slotDir = "/tmp/${CLUSTER_NAME}-${slot}"
    def operatorNs = 'psmdb-operator'
    if (!test.ownCluster) {
        def suffix = sh(script: "head -c 3 /dev/urandom | od -An -tx1 | tr -d ' \\n'", returnStdout: true).trim()
        operatorNs = "${testName}-${suffix}-op"
    }
    // One folder per attempt: finishSlotAllureResults labels its results with the cluster and slot.
    def allureDir = "e2e-tests/allure-results-slots/${testName}-${new Date().getTime()}"

    sh """
        rm -rf '${slotDir}'
        mkdir -p '${slotDir}'
        cp '${clusterKubeconfig}' '${slotDir}/kubeconfig'
    """
    def slotExports = """
        export KUBECONFIG='${slotDir}/kubeconfig'
        export HELM_CONFIG_HOME='${slotDir}/helm'
        export HELM_CACHE_HOME='${slotDir}/helm-cache'
        export DELETE_CRD_ON_START=0
        export CLEAN_NAMESPACE=0
        export SKIP_DELETE=1
        export OPERATOR_NS='${operatorNs}'
        export ALLURE_RESULTS_DIR='${allureDir}'
        ${test.ownCluster ? '' : 'export SHARED_CLUSTER=1'}
    """

    runTest(testId, slotExports, operatorNs, false)
    finishSlotAllureResults(allureDir, test.cluster, slot)

    def failed = test.result != 'passed'
    if (failed && !test.ownCluster && currentBuild.nextBuild == null) {
        echo "The $testName test failed next to other tests, it will be retried alone on a cluster"
        // Keep the log of this attempt: the retry writes the test's log again.
        sh """
            cp e2e-tests/logs/${testName}.log e2e-tests/logs/${testName}-shared.log 2>/dev/null || true
            cp e2e-tests/logs/${testName}-debug.log e2e-tests/logs/${testName}-shared-debug.log 2>/dev/null || true
        """
        pushLogFile("${testName}-shared")
        pushLogFile("${testName}-shared-debug")
        test.sharedTime = test.time
        test.requeueAlone = true
    } else {
        if (test.retriedAlone) {
            test.time = (test.time as Double) + (test.sharedTime as Double)
            if (!failed) {
                echo "The $testName test passed alone after it failed next to other tests. If this repeats, tag it ownCluster or with a lock in e2e-tests/tests.yaml"
            }
        }
        if (failed) {
            currentBuild.result = 'FAILURE'
        }
    }

    try {
        cleanupSlotTest(clusterKubeconfig, testName, test.ownCluster)
    } catch (org.jenkinsci.plugins.workflow.steps.FlowInterruptedException exc) {
        throw exc
    } catch (cleanupErr) {
        echo "Warning: failed to clean up after ${testName}: ${cleanupErr}"
    }
}

// Frees the cluster for the next tests: deletes the test's own namespaces and, in cluster-wide
// runs, its ClusterRole and ClusterRoleBinding. After a test that ran alone also removes
// cert-manager (other tests expect the operator to issue its own certificates, which it does only
// without cert-manager) and its operator, which watches every namespace. Only the operator's own
// resources get their finalizers removed: they keep a namespace Terminating.
void cleanupSlotTest(String kubeconfig, String testName, Boolean ownCluster) {
    withEnv(["KUBECONFIG=${kubeconfig}", "TEST_NAME=${testName}", "OWN_CLUSTER=${ownCluster ? 1 : 0}"]) {
        sh '''
            set +e
            if ! kubectl get --raw='/healthz' --request-timeout=5s >/dev/null 2>&1; then
                echo "Skipping the cleanup after $TEST_NAME: the cluster is not reachable"
                exit 0
            fi

            resources=$(kubectl api-resources --api-group=psmdb.percona.com --namespaced -o name --request-timeout=10s | paste -sd, -)
            kubectl get namespaces --request-timeout=10s --no-headers -o custom-columns=NAME:.metadata.name \
                | grep -E "^${TEST_NAME}-[0-9a-f]+(-.*)?\\$" \
                | while read -r namespace; do
                    echo "Deleting namespace: $namespace"
                    if [ -n "$resources" ]; then
                        kubectl get "$resources" -n "$namespace" -o name --request-timeout=10s 2>/dev/null \
                            | while read -r object; do
                                kubectl patch "$object" -n "$namespace" --type=merge -p '{"metadata":{"finalizers":[]}}' --request-timeout=10s
                            done
                    fi
                    kubectl delete namespace "$namespace" --wait=false --request-timeout=10s
                done
            kubectl get clusterrole,clusterrolebinding --request-timeout=10s -o name \
                | grep -E -- "-${TEST_NAME}-[0-9a-f]+-op\\$" \
                | xargs -r kubectl delete --ignore-not-found --wait=false --request-timeout=10s

            if [ "$OWN_CLUSTER" != 1 ]; then
                exit 0
            fi
            kubectl delete namespace psmdb-operator --ignore-not-found --timeout=120s
            kubectl delete clusterrolebinding service-account-percona-server-mongodb-operator --ignore-not-found --request-timeout=30s
            kubectl delete clusterrole percona-server-mongodb-operator --ignore-not-found --request-timeout=30s

            selector=app.kubernetes.io/instance=cert-manager
            if kubectl get namespace cert-manager --request-timeout=10s >/dev/null 2>&1 \
                || [ -n "$(kubectl get crd -l "$selector" -o name --request-timeout=10s 2>/dev/null)" ]; then
                echo "Removing cert-manager left by $TEST_NAME"
                kubectl delete mutatingwebhookconfiguration,validatingwebhookconfiguration -l "$selector" --ignore-not-found --request-timeout=30s
                kubectl delete clusterrole,clusterrolebinding -l "$selector" --ignore-not-found --request-timeout=30s
                kubectl delete role,rolebinding -n kube-system -l "$selector" --ignore-not-found --request-timeout=30s
                # Wait: the operator decides how to issue certificates by whether these CRDs exist.
                kubectl delete crd -l "$selector" --ignore-not-found --timeout=180s
                kubectl delete namespace cert-manager --ignore-not-found --timeout=180s
            fi
            exit 0
        '''
    }
}

// Returns the next test a slot on this cluster may start, -1 when tests are left but none may
// start here yet, or null when no test is left.
// - ownCluster tests are taken first, then tests with locks, then the rest, so the tests that need
//   a cluster alone start while the clusters are empty.
// - An ownCluster test starts only on a cluster with nothing running, and nothing starts next to
//   it. While one is waiting, busy clusters start nothing new, so it is not starved.
// - A test with locks does not start while a test with one of those locks runs on the cluster.
@NonCPS
Integer claimNextTest(String cluster) {
    synchronized (tests) {
        def running = []
        def held = []
        def pending = false
        for (test in tests) {
            if (test.result == 'skipped') {
                pending = true
            } else if (test.running && test.cluster == cluster) {
                running << test
                for (lock in (test.locks ?: [])) {
                    held << lock
                }
            }
        }
        if (!pending) {
            return null
        }
        for (test in running) {
            if (test.ownCluster) {
                return -1
            }
        }

        for (int group = 0; group < 3; group++) {
            for (int i = 0; i < tests.size(); i++) {
                def test = tests[i]
                def testGroup = test.ownCluster ? 0 : (test.locks ? 1 : 2)
                if (test.result != 'skipped' || testGroup != group) {
                    continue
                }
                if (test.ownCluster) {
                    if (running) {
                        return -1
                    }
                } else if ((test.locks ?: []).any { held.contains(it) }) {
                    continue
                }
                test.result = 'failure'
                test.cluster = cluster
                test.running = true
                return i
            }
        }
        return -1
    }
}

// Clears running under the lock. A test marked for a retry alone goes back to the queue as an
// ownCluster test here, after running is cleared, so no slot claims the retry while this slot
// still counts the test as running.
@NonCPS
void markTestFinished(Integer testId) {
    synchronized (tests) {
        def test = tests[testId]
        test.running = false
        if (test.requeueAlone) {
            test.requeueAlone = false
            test.retriedAlone = true
            test.ownCluster = true
            test.result = 'skipped'
        }
    }
}

@NonCPS
Integer countSkippedTests() {
    synchronized (tests) {
        return tests.count { it.result == 'skipped' } as Integer
    }
}

// operatorNs: the namespace of the test's operator, or '' to find it.
void collectDebugLogs(String testName, String clusterSuffix, String operatorNs) {
    try {
        sh """
            export KUBECONFIG=/tmp/${CLUSTER_NAME}-${clusterSuffix}
            export PYTEST_NAMESPACE_FILE=/tmp/pytest_current_namespace-${testName}-${clusterSuffix}
            DEBUG_LOG=e2e-tests/logs/${testName}-debug.log

            {
                echo "=== DEBUG LOGS FOR ${testName} (collected at \$(date -u)) ==="

                NAMESPACE=""
                if [ -f "\$PYTEST_NAMESPACE_FILE" ]; then
                    NAMESPACE=\$(cat "\$PYTEST_NAMESPACE_FILE" | tail -1)
                fi
                if [ -z "\$NAMESPACE" ]; then
                    NAMESPACE=\$(kubectl get namespaces --no-headers 2>/dev/null | awk '{print \$1}' | grep -E '^${testName}-[0-9a-f]+\$' | head -1)
                fi
                if [ -z "\$NAMESPACE" ]; then
                    NAMESPACE=\$(kubectl get namespaces --no-headers 2>/dev/null | awk '{print \$1}' | grep -vE '^kube-|^default\$|^gke-|^gmp-' | head -1)
                fi
                echo "Namespace: \$NAMESPACE"

                echo ""
                echo "=== PSMDB Objects ==="
                kubectl get psmdb --all-namespaces -o wide 2>&1 || true

                if [ -n "\$NAMESPACE" ]; then
                    echo ""
                    echo "=== PSMDB Describe ==="
                    for obj in \$(kubectl get psmdb -n "\$NAMESPACE" -o name 2>/dev/null); do
                        echo "--- \$obj ---"
                        kubectl describe -n "\$NAMESPACE" "\$obj" 2>&1 || true
                    done

                    echo ""
                    echo "=== StatefulSets ==="
                    kubectl get sts -n "\$NAMESPACE" -o wide 2>&1 || true
                    for obj in \$(kubectl get sts -n "\$NAMESPACE" -o name 2>/dev/null); do
                        echo "--- \$obj ---"
                        kubectl describe -n "\$NAMESPACE" "\$obj" 2>&1 || true
                    done

                    echo ""
                    echo "=== Pods ==="
                    kubectl get pods -n "\$NAMESPACE" -o wide 2>&1 || true

                    echo ""
                    echo "=== Pod Events ==="
                    kubectl get events -n "\$NAMESPACE" --sort-by='.lastTimestamp' 2>&1 || true

                    echo ""
                    echo "=== Operator Logs (last 100 lines) ==="
                    OPERATOR_NS='${operatorNs}'
                    if [ -z "\$OPERATOR_NS" ]; then
                        OPERATOR_NS=\$(kubectl get deployments --all-namespaces 2>/dev/null | grep percona-server-mongodb-operator | awk '{print \$1}' | head -1)
                    fi
                    if [ -n "\$OPERATOR_NS" ]; then
                        OPERATOR_POD=\$(kubectl get pods -n "\$OPERATOR_NS" -l name=percona-server-mongodb-operator -o name 2>/dev/null | head -1)
                        if [ -n "\$OPERATOR_POD" ]; then
                            kubectl logs -n "\$OPERATOR_NS" "\$OPERATOR_POD" --tail=100 2>&1 || true
                        fi
                    fi
                fi

                echo ""
                echo "=== Cert-Manager Resources ==="
                kubectl get crd 2>/dev/null | grep cert-manager || echo "No cert-manager CRDs"
                kubectl get validatingwebhookconfiguration 2>/dev/null | grep cert-manager || echo "No cert-manager validating webhooks"
                kubectl get mutatingwebhookconfiguration 2>/dev/null | grep cert-manager || echo "No cert-manager mutating webhooks"
            } > "\$DEBUG_LOG" 2>&1
        """
    } catch (debugExc) {
        echo "Failed to collect debug logs for $testName: ${debugExc.message}"
    }
}

void runTest(Integer TEST_ID) {
    runTest(TEST_ID, '', '', true)
}

// slotExports: shell exports for a test that shares the cluster (runSlotTest).
// failBuild: false when the caller decides the build result, after a possible retry.
void runTest(Integer TEST_ID, String slotExports, String operatorNs, Boolean failBuild) {
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
                export PYTEST_NAMESPACE_FILE=/tmp/pytest_current_namespace-${testName}-${clusterSuffix}
                export GCP_PROJECT=\$GCP_PROJECT
                export GCS_WI_SERVICE_ACCOUNT=percona-psmdb-operator-wi@\$GCP_PROJECT.iam.gserviceaccount.com
                export PATH="\$HOME/.local/bin:\$PATH"
                ${slotExports}
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
            long elapsedMin = ((long)(new Date().getTime() - timeStart)).intdiv(60000)
            echo "Test $testName timed out after ${elapsedMin} minutes!"
            collectDebugLogs(testName, clusterSuffix, operatorNs)
            tests[TEST_ID]["result"] = "error"
            if (failBuild) {
                currentBuild.result = 'FAILURE'
            }
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
        long elapsedMin = ((long)(new Date().getTime() - timeStart)).intdiv(60000)
        if (exc.message?.contains('exit code 143')) {
            echo "Test $testName was terminated (exit 143) after ${elapsedMin} minutes - treating as timeout/error!"
            collectDebugLogs(testName, clusterSuffix, operatorNs)
            tests[TEST_ID]["result"] = "error"
        } else {
            echo "Test $testName has failed after ${elapsedMin} minutes!"
            collectDebugLogs(testName, clusterSuffix, operatorNs)
            tests[TEST_ID]["result"] = "failure"
        }
        if (failBuild) {
            currentBuild.result = 'FAILURE'
        }
    }
    finally {
        def timeStop = new Date().getTime()
        def durationSec = (timeStop - timeStart) / 1000
        tests[TEST_ID]["time"] = durationSec
        pushLogFile("$testName")
        pushLogFile("${testName}-debug")
        long totalSec = ((long)(timeStop - timeStart)).intdiv(1000)
        long mins = totalSec.intdiv(60)
        long secs = totalSec % 60
        echo "The $testName test was finished in ${totalSec}s (${mins}m ${secs}s)!"
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

def skipRequested() {
    try {
        if (pullRequest.labels.contains('skip-e2e-tests')) {
            echo "PR has the 'skip-e2e-tests' label. Skipping e2e tests."
            return true
        }
    } catch (Exception e) {
        echo "Could not read PR labels: ${e.message}"
    }
    return false
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

    if (skipRequested()) {
        needToRunTests = false
        return
    }
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
                prepareNode()
                initTests()
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
                            golang:1.27 sh -c '
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
                            golang:1.27 sh -c 'go build -v -o percona-server-mongodb-operator github.com/percona/percona-server-mongodb-operator/cmd/manager'
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
                timeout(time: 7, unit: 'HOURS')
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

                        // pytest_html_merger needs at least one real pytest-html report. When every test
                        // was already passed in a previous build, only placeholders exist and it fails.
                        def hasHtmlReports = sh(returnStatus: true, script: "grep -qs 'Report generated on ' e2e-tests/reports/*.html") == 0

                        sh """
                            export PATH="\$HOME/.local/bin:\$PATH"
                            uv run junitparser merge --glob 'e2e-tests/reports/*.xml' ${reportXml}
                        """
                        junit testResults: reportXml, healthScaleFactor: 1.0
                        archiveArtifacts "${reportXml}"
                        writeClusterLoadEnvironment()
                        def allurePublished = publishAllureReport()

                        unstash 'IMAGE'
                        def IMAGE = sh(returnStdout: true, script: "cat results/docker/TAG").trim()
                        TestsReport = TestsReport + "\r\n\r\nCommit: ${env.CHANGE_URL}/commits/${env.GIT_COMMIT}\r\nImage: `${IMAGE}`\r\n"

                        if (hasHtmlReports) {
                            sh """
                                export PATH="\$HOME/.local/bin:\$PATH"
                                uv run pytest_html_merger -i e2e-tests/reports -o ${reportHtml} -t "PSMDB e2e tests - ${env.GIT_BRANCH} (${env.GIT_SHORT_COMMIT})"
                            """
                            formatReportDuration(reportHtml)
                            archiveArtifacts "${reportHtml}"
                            pushReportFile()

                            def reportUrl = "${testUrlPrefix}/${env.GIT_BRANCH}/${env.GIT_SHORT_COMMIT}/${reportHtml}"
                            currentBuild.description = "<a href=\"${reportUrl}\">Test report</a>"
                            TestsReport = TestsReport + "Test report: [report](${reportUrl})\r\n"
                        } else {
                            echo "No pytest-html reports in e2e-tests/reports, skipping the HTML report merge"
                        }
                        if (allurePublished) {
                            TestsReport = TestsReport + "Allure report: [allure](${env.BUILD_URL}allure/) (tests that ran and finished in this build only; see the table above for timeouts)\r\n"
                        }
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
