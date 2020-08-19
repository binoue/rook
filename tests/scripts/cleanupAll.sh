#!/bin/bash

# This is a debuging script

echo "Check crds"
kubectl get crd

echo "Cleanup all crds if exist"
kubectl delete crd --all

echo "Check disks / lv / vg"
test_scratch_device=/dev/xvdc
test_scratch_device2=/dev/xvdd
if [ $# -ge 1 ] ; then
  test_scratch_device=$1
fi
if [ $# -ge 2 ] ; then
  test_scratch_device2=$2
fi
sudo hexdump -c -n 128 ${test_scratch_device}
sudo hexdump -c -n 128 ${test_scratch_device2}

sudo lvs
sudo vgs

echo "Clean up lvs / vgs if exists"
sudo vgs -o vg_name | tail -n +2 | sudo xargs vgremove -f

echo "Clean up disks"
sudo dd if=/dev/zero of=${test_scratch_device} bs=1M count=100 oflag=direct
sudo dd if=/dev/zero of=${test_scratch_device2} bs=1M count=100 oflag=direct
