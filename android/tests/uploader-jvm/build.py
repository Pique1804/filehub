#!/usr/bin/env python3
"""Compile the actual uploader after Gradle has resolved its pinned dependencies."""
from pathlib import Path
import os
import subprocess

root = Path(__file__).resolve().parent
project = root.parents[1]
cache = Path(os.environ.get("GRADLE_USER_HOME", str(Path.home() / ".gradle"))) / "caches/modules-2/files-2.1"
output = project / "app/build/uploader-jvm"
output.mkdir(parents=True, exist_ok=True)

def jar(group, name, version):
    paths = sorted((cache / group / name / version).glob("*/*.jar"))
    if not paths:
        raise SystemExit(f"Missing {group}:{name}:{version}; run Gradle testDebugUnitTest first")
    return paths[0]

stdlib = jar("org.jetbrains.kotlin", "kotlin-stdlib", "1.9.24")
annotations = jar("org.jetbrains", "annotations", "23.0.0")
compiler = [jar("org.jetbrains.kotlin", "kotlin-compiler-embeddable", "1.9.24"), stdlib,
            jar("org.jetbrains.kotlin", "kotlin-reflect", "1.6.10"),
            jar("org.jetbrains.intellij.deps", "trove4j", "1.0.20200330"), annotations]
deps = [stdlib, annotations, jar("com.squareup.okhttp3", "okhttp", "4.12.0"),
        jar("com.squareup.okio", "okio-jvm", "3.8.0"),
        jar("org.jetbrains.kotlinx", "kotlinx-coroutines-core-jvm", "1.7.3"),
        jar("org.json", "json", "20240303")]
classpath = os.pathsep.join(map(str, deps))
subprocess.run(["java", "-cp", os.pathsep.join(map(str, compiler)),
                "org.jetbrains.kotlin.cli.jvm.K2JVMCompiler", "-jvm-target", "17", "-no-stdlib", "-no-reflect",
                "-classpath", classpath, "-d", str(output / "harness.jar"),
                str(project / "app/src/main/java/com/filehub/backup/ResumableUploader.kt"),
                *map(str, sorted(root.glob("*.kt")))], check=True)
(output / "classpath.txt").write_text(str(output / "harness.jar") + os.pathsep + classpath)
print(f"Compiled current ResumableUploader.kt; classpath: {output / 'classpath.txt'}")
