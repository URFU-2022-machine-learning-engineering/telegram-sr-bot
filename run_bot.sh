#!/bin/bash

docker stop $CONTAINER_NAME
docker build -t $IMAGE_NAME:$TAG .
docker run -d --rm --env-file $ENV_FILE --publish "2112:2112" --name $CONTAINER_NAME $IMAGE_NAME:$TAG
