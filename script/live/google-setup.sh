#!/usr/bin/env bash
# Enables the Google APIs pimvault's Google app uses in its Google Cloud
# project. The end-to-end test tokens live in Azure Key Vault (D202), not
# in Google Cloud. Run it in Google Cloud Shell with the pimvault project
# selected.
set -euo pipefail

project=$(gcloud config get-value project 2>/dev/null)
if [ -z "${project}" ]; then
  echo "select the project first: gcloud config set project <project id>" >&2
  exit 1
fi

gcloud services enable gmail.googleapis.com calendar-json.googleapis.com people.googleapis.com \
  tasks.googleapis.com
