#!/bin/bash

gcloud functions deploy --runtime go113 --trigger-http --allow-unauthenticated HandleGHWebhook