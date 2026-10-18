# Code style

## Comments

Write one sentence per line, and do not wrap a sentence at a fixed column.
A diff then shows the sentence that changed, and the editor wraps long lines.

## Tests

Use testify.
Use `require` when the test cannot continue after a failure, and `assert` for all other checks.

When possible, compare the full value with one `assert.Equal`, and not each field separately.
When the struct gets a new field, the test fails until somebody adds the field to the expected value.
The test then stays complete, and does not slowly check less and less of the code.
It also shows the full expected value in one place, and testify shows the diff of all the fields when it fails.

Compare the full error message with `assert.EqualError`, and not a part of it with `assert.Contains`.
A full comparison cannot give a false positive, and it checks what the user sees.
