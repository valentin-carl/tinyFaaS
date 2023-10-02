#!/usr/bin/env python3
import os
import sys

# removes the '.sh' from every filename
get_commands = lambda path : [s[:-3] for s in os.listdir(path)]

if __name__ == '__main__':

    # get the program inputs
    args = sys.argv

    # the filename of this program is the only argument => nothing to do
    if len(args) == 1:
        sys.exit(1)

    # get the available tinyFaaS commands
    path = "./scripts"
    commands = get_commands(path)

    # validate that the respective shell script exists (inputs for that aren't validated
    tf_command = args[1] + ".sh"
    if tf_command[:-3] not in commands:
        sys.exit(1)

    # build the shell cmd
    for arg in args:
        if arg[:1] == "-" or arg[-3:] == ".py" or arg == tf_command[:-3]:
            continue
        else:
            tf_command += f" '{arg}'"

    # execute cmd
    os.system("./scripts/" + tf_command)
