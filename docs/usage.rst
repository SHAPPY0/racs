Usage
=====

Login / Logout
--------------

By default, ``racs`` requires users to login before performing certain operations. Users can login by clicking :guilabel:`LOGIN` in the top bar and entering their credentials. Currently ``racs`` uses `PAM <https://en.wikipedia.org/wiki/Pluggable_authentication_module>`_ for authentication, effectively users are authenicated against the underlying operating system.

Projects Overview
-----------------

Each buildable unit in ``racs`` is called a *project*. Each project is assigned an increment integer identifier, starting at 1. Projects are stored in the :file:`/projects` directory with the following structure:

.. folders::
   
   + projects
     + 1
       + context
       + workspace
         + source
     + 2
       + context
       + workspace
         + source
     + ...

Each project directory has the following contents:

:*/context*: is the context for the ``prepare`` and ``package`` stages.
:*/workspace*: is the working directory for the **build** and **package** stages.
:*/workspace/source*: is the cloned source directory.

.. note::

   The :file:`/workspace` directory for each project is preserved between builds and mounted automatically as :file:`/workspace` during the **build** and **package** stages. This allows for builds to be incremental since build output is reused. The **clean** stage can be used to clear the :file:`/workspace/source` directory.

Creating Projects
-----------------

Projects can be created by clicking :guilabel:`CREATE PROJECT` in the top bar (logging in first if necessary). A dialog appears for entering the new project's details. Note that all the details can also be entered or changed later.

:Name: The name of the project, for display purposes only.
:URL: The URL of the git repository for the project.
:Branch: The git branch to clone / pull.
:Destination: *Optional* An OCI container registry to push the built image.
:Tag: *Optional* A template for the image tag when pushing to an OCI container registry.

The project tag can contain variables of the form :samp:`${NAME}` which are substituted when an image is created:

:``$VERSION``: Replaced with the latest successful build version, incremented automatically, starting from 1.

After creating a project, at least 2 additional files need to be uploaded before the project can be built.

Project Uploads
---------------

Additional files can be uploaded to a project's directory. Users can open the project settings dialog by clicking the :fas:`tools` button and then switching to the :guilabel:`Upload` tab. Files can be uploaded to any path in the project's directory.

Container Spec Files
....................

``racs`` requires 2 OCI container spec files to be available somewhere in the project directory for creating the build image and package image for the project. These files can be located anywhere in the project directory and named anything but by default are expected to reside at :file:`/BuildSpec` and :file:`/PackageSpec` respectively. This allows ``racs`` to be used to build projects which do not contain the necessary container spec files in their repositories.

The paths of the build and package spec files can be changed using the project settings dialog by clicking the :fas:`tools` button and switching to the :guilabel:`Settings` tab. For projects that keep the build and package spec files within the git repository, these paths can be changed to something like :file:`/workspace/source/BuildSpec` and :file:`/workspace/source/PackageSpec`. 

Build Stages
------------

Every project has a fixed set of build stages. After each stage is complete, the next stage is automatically started. Users can manually restart the build process from a specific using the :guilabel:`--Build--` dropdown for each project.

:Clean: Deletes the project's :file:`/workspace/source` directory.
:Clone: Recursively clones the selected branch of the project's git repository into a directory called :file:`/source`.
:Prepare: Builds the OCI container (using :file:`BuildSpec`) that will be used for building / updating the project when required.
:Pull: Recursively pulls the latest changes from the git repository. This is the default starting point for each subsequent build after the initial build.
:Build: Runs the build image with the :file:`/workspace` directory mounted. The build image's ``ENTRYPOINT`` should be the build command for the project.
:Package: Builds the OCI container (using :file:`PackageSpec`) that will be tagged and pushed to the remote registry.
:Push: Pushes the package image to the remote registry. If no destination is specified for this project then this stage does nothing.

Project Version
---------------

Each time a project's package stage completes successfully, it's version is incremented. This can be used in the image tag when pushing to a container registry by using ``$VERSION`` in the project tag setting.

Triggers
--------

Each time a project's push stage completes successfully, it can trigger other projects to start building from a specified stage. Triggers can be configured for a project by clicking the :fas:`tools` buttons an switching to the :guilabel:`Triggers` tab.

When triggered from another project, the additional environment variable ``RACS_TRIGGER`` is passed to the build stage with the triggering project's tag value.
