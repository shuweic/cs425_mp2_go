#!/bin/bash

for val in 0{1..9} 10
do
    echo VM$val
    ssh shuweic3@fa24-cs425-87$val.cs.illinois.edu "cd cs425_g87; git pull; go build; exit"
done
echo 'Git Update!'

