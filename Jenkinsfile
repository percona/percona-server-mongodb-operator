pipeline {
  environment {
          CLOUDSDK_CORE_DISABLE_PROMPTS = 1
          CLEAN_NAMESPACE = 1
          GIT_SHORT_COMMIT = sh(script: 'git describe --always --dirty', , returnStdout: true).trim()
          VERSION = "${env.GIT_BRANCH}-${env.GIT_SHORT_COMMIT}"
          CLUSTER_NAME = sh(script: "echo jenkins-psmdb-${GIT_SHORT_COMMIT} | tr '[:upper:]' '[:lower:]'", , returnStdout: true).trim()
          AUTHOR_NAME  = sh(script: "echo ${CHANGE_AUTHOR_EMAIL} | awk -F'@' '{print \$1}'", , returnStdout: true).trim()
          ENABLE_LOGGING="true"
  }
  agent {
    label 'docker'
  }
  stages {
    stage('Test notifications') {
      steps {
        script {
          echo "Test"
          try {
            slackSend channel: "@${AUTHOR_NAME}", color: '#FF0000', message: "[${JOB_NAME}]: build ${currentBuild.result}, ${BUILD_URL} owner: @${AUTHOR_NAME}"
          }
          catch (exc) {
            echo "Error"
//             slackSend channel: '#cloud-dev-ci', color: '#FF0000', message: "[${JOB_NAME}]: build ${currentBuild.result}, ${BUILD_URL} owner: @${AUTHOR_NAME}"
          }
        }
      }
    }
  }
}