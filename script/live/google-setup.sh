#!/usr/bin/env bash
# Sets up pimvault's Google Cloud project for end-to-end tests (D193):
# enables the APIs, creates the test token secrets (D158) and a service
# account that may read them and add new versions, and prints that account's
# key. Run it in Google Cloud Shell with the pimvault project selected.
set -euo pipefail

project=$(gcloud config get-value project 2>/dev/null)
if [ -z "${project}" ]; then
  echo "select the project first: gcloud config set project <project id>" >&2
  exit 1
fi

gcloud services enable gmail.googleapis.com calendar-json.googleapis.com people.googleapis.com \
  tasks.googleapis.com secretmanager.googleapis.com iam.googleapis.com

account="pimvault-e2e@${project}.iam.gserviceaccount.com"
if ! gcloud iam service-accounts describe "${account}" >/dev/null 2>&1; then
  gcloud iam service-accounts create pimvault-e2e --display-name="pimvault end-to-end tests"
fi

for secret in e2e-gmail-token e2e-outlook-token; do
  if ! gcloud secrets describe "${secret}" >/dev/null 2>&1; then
    gcloud secrets create "${secret}" --replication-policy=automatic
  fi

  # Read versions, add new ones and destroy old ones, so refreshed tokens
  # replace earlier versions instead of piling up.
  for role in roles/secretmanager.secretAccessor roles/secretmanager.secretVersionManager; do
    gcloud secrets add-iam-policy-binding "${secret}" --member="serviceAccount:${account}" \
      --role="${role}" >/dev/null
  done
done

key=$(mktemp)
trap 'rm -f "${key}"' EXIT
gcloud iam service-accounts keys create "${key}" --iam-account="${account}" >/dev/null

echo
echo "Store this JSON as PIMVAULT_E2E_SERVICE_ACCOUNT: a repository Actions secret and the agent's environment variable:"
echo
tr -d '\n' <"${key}"
echo
