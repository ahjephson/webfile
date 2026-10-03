# Future tasks

## Real 7-Zip CLI integration coverage

Add a Linux integration test that exercises WebFile against an actual `7zz`
or `7z` executable rather than a shell-script substitute. The test should:

- create a real encrypted archive;
- verify encrypted-archive detection and password submission through stdin;
- cover correct and incorrect password handling;
- assert that extraction progress is reported before completion;
- verify the extracted content and staging cleanup; and
- skip when run locally without 7-Zip, while remaining required in CI where a
  supported 7-Zip version is installed explicitly.
