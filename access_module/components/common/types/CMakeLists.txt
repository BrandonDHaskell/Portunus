# common/types — Dependency-free shared type definitions
#
# This component has no source files and no dependencies beyond ESP-IDF base
# types, ensuring it can be included by any layer without introducing cycles.

idf_component_register(
    INCLUDE_DIRS
        "include"
)