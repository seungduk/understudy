# Guide test: five scripts to write

Read only SCRIPTS.md (the understudy script guide). Write each script as YAML into the folder
you are given, with exactly these file names. Do not run anything; write the files and stop.

1. `1-text.yaml`. A request that names no session gets the final answer, exactly:
   `PLAN: read, fix, test`
2. `2-edit.yaml`. Session `t1/writer`. The working directory holds `notes.txt` whose content is
   `color: red`. The model reads `notes.txt`, then edits it so `red` becomes `blue`, then gives
   the final answer, exactly: `DONE`
3. `3-branch.yaml`. Session `t2/writer`. The model runs a shell command that checks whether a
   file named `ready` exists in the working directory. If it exists, the final answer is exactly
   `PASS`. If it does not, the model writes the file `ready` (any content), runs the check
   again, and then judges that result the same way.
4. `4-limit.yaml`. Session `t3/writer`. The very first request gets a weekly usage limit that
   resets in two hours.
5. `5-stall.yaml`. Session `t4/reviewer`. The final answer is exactly `CAUSE: WORK`, but it is
   held back until a gate named `review-gate` is released.
