

# How to run

To set up rbac (role-based access control) permissions run

```bash
curl https://raw.githubusercontent.com/JorritSalverda/evohome-hgi80-listener/master/k8s/rbac.yaml | kubectl apply -f -
```

In order to configure the application run

```bash
curl https://raw.githubusercontent.com/JorritSalverda/evohome-hgi80-listener/master/k8s/configmap.yaml | EVOHOME_ID='id-of-the-evohome-touch' envsubst \$EVOHOME_ID | kubectl apply -f -
```

And for deploying a new version or changing the schedule run

```bash
curl https://raw.githubusercontent.com/JorritSalverda/evohome-hgi80-listener/master/k8s/deployment.yaml | HGI_USB_PATH="/dev/ttyUSB0" CONTAINER_TAG='0.1.5' envsubst \$HGI_USB_PATH,\$CONTAINER_TAG | kubectl apply -f -
```
