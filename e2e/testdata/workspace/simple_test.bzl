def _simple_test_impl(ctx):
    script = ctx.actions.declare_file(ctx.label.name + ".sh")
    ctx.actions.write(
        output = script,
        content = "#!/bin/sh\nset -eu\nexit 0\n",
        is_executable = True,
    )
    return [DefaultInfo(executable = script)]

simple_test = rule(
    implementation = _simple_test_impl,
    test = True,
)
