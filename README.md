# OPipes - Higher order pipes

OPipes is a command wrapper (`o`) that can take a bash pipeline used to filter local logs and make it work for any cloud provider without needing to change the pipeline, such that the logs are filtered in the cloud first instead of downloaded all locally... and does so without quoting the pipeline!

To install:

```shell
go install github.com/lucabrunox/opipes/cmd/o@v0.1.3
```

To enable debug mode:

```shell
export OLOGLEVEL=debug
```

### Example with AWS Logs

Set up a log group with some test logs (skip this if you already have logs to work with):

```shell
aws logs create-log-group --log-group-name test-log1
aws logs create-log-stream --log-group-name test-log1 --log-stream-name test-stream1
aws logs put-log-events --log-group-name test-log1 --log-stream-name test-stream1 --log-events "timestamp=$(date +%s)000,message=INFO myapp: some info message"
aws logs put-log-events --log-group-name test-log1 --log-stream-name test-stream1 --log-events "timestamp=$(date +%s)000,message=ERROR myapp: some error message"
```

After a few minutes you should see logs flowing:

```shell
aws logs filter-log-events --log-group-name test-log1 --log-stream-name-prefix test
```

Now let's say you want to filter by "ERROR" and by "myapp". Normally you would do this with a `| grep ERROR | grep myapp` pipeline, however that means that all the logs need to be streamed locally.

Instead by prefixing all the commands in the pipeline with `o`, opipes will automatically detect the two greps, and replace a place-holder with AWS-specific log filtering for you, so that the filtering is done in the cloud. Try it:

```shell
# Before
aws logs filter-log-events \
  --log-group-name test-log1 --log-stream-name-prefix test \
  | grep ERROR \
  | grep myapp

# After
o aws logs filter-log-events \
  --log-group-name test-log1 --log-stream-name-prefix test \
  --filter-pattern {awsLogFilter} \
  | o grep ERROR \
  | o grep myapp
```

In debug mode it will show that `{awsLogFilter}` is being replaced with `"ERROR myapp"`:

```shell
time=2025-08-29T17:59:04.336+02:00 level=DEBUG msg="starting command" program=aws pid=1124819 args="[aws logs filter-log-events --log-group-name test-log1 --log-stream-name-prefix test --filter-pattern  ERROR myapp]"
```

Only basic grep filters with one string are supported at the moment. To know whether `o` is able to push-down the filters up to the source enable the debug mode with `export OLOGLEVEL=debug`.
