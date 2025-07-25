package distro

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/targets/linux"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func (cfg *Config) BuildContainer(ctx context.Context, client gwclient.Client, worker llb.State, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, rpmDir llb.State, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, dalec.ProgressGroup("Install RPMs"))
	const workPath = "/tmp/rootfs"

	bi, err := spec.GetSingleBase(targetKey)
	if err != nil {
		return llb.Scratch(), err
	}

	skipBase := bi != nil
	rootfs, err := bi.ToState(sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	installTimeRepos := spec.GetInstallRepos(targetKey)
	repoMounts, keyPaths, err := cfg.RepoMounts(installTimeRepos, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}
	importRepos := []DnfInstallOpt{DnfWithMounts(repoMounts), DnfImportKeys(keyPaths)}

	rpmMountDir := "/tmp/rpms"

	installOpts := []DnfInstallOpt{DnfAtRoot(workPath)}
	installOpts = append(installOpts, importRepos...)
	installOpts = append(installOpts, []DnfInstallOpt{
		DnfNoGPGCheck,
		IncludeDocs(spec.GetArtifacts(targetKey).HasDocs()),
		dnfInstallWithConstraints(opts),
	}...)

	baseMountPath := rpmMountDir + "-base"
	basePkgs := llb.Scratch().File(llb.Mkdir("/RPMS", 0o755))
	pkgs := []string{
		filepath.Join(rpmMountDir, "**/*.rpm"),
	}

	if !skipBase && len(cfg.BasePackages) > 0 {
		opts := append(opts, dalec.ProgressGroup("Create base virtual package"))

		var basePkgStates []llb.State
		for _, spec := range cfg.BasePackages {
			pkg, err := cfg.BuildPkg(ctx, client, worker, sOpt, &spec, targetKey, opts...)
			if err != nil {
				return llb.Scratch(), errors.Wrap(err, "error building base runtime deps package")
			}
			basePkgStates = append(basePkgStates, pkg)
		}

		basePkgs = dalec.MergeAtPath(basePkgs, basePkgStates, "/")
		pkgs = append(pkgs, filepath.Join(baseMountPath, "**/*.rpm"))
	}

	rootfs = worker.Run(
		cfg.Install(pkgs, installOpts...),
		llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS")),
		llb.AddMount(baseMountPath, basePkgs, llb.SourcePath("/RPMS")),
		dalec.WithConstraints(opts...),
	).AddMount(workPath, rootfs)

	if post := spec.GetImagePost(targetKey); post != nil && len(post.Symlinks) > 0 {
		rootfs = rootfs.With(dalec.InstallPostSymlinks(post, worker, opts...))
	}

	return rootfs, nil
}

func (cfg *Config) HandleDepsOnly(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		deps := spec.GetRuntimeDeps(targetKey)
		if len(deps) == 0 {
			return nil, nil, fmt.Errorf("no runtime deps found for '%s'", targetKey)
		}

		pg := dalec.ProgressGroup("Build " + targetKey + " deps-only container for: " + spec.Name)

		sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return nil, nil, err
		}

		pc := dalec.Platform(platform)
		worker, err := cfg.Worker(sOpt, pg, pc)
		if err != nil {
			return nil, nil, err
		}

		var rpmDir = llb.Scratch()

		if len(deps) > 0 {
			withDownloads := worker.Run(dalec.ShArgs("set -ex; mkdir -p /tmp/rpms/RPMS/$(uname -m)")).
				Run(cfg.Install(spec.GetRuntimeDeps(targetKey),
					DnfDownloadAllDeps("/tmp/rpms/RPMS/$(uname -m)"))).Root()
			rpmDir = llb.Scratch().File(llb.Copy(withDownloads, "/tmp/rpms", "/", dalec.WithDirContentsOnly()))
		}
		ctr, err := cfg.BuildContainer(ctx, client, worker, sOpt, spec, targetKey, rpmDir, pg, pc)
		if err != nil {
			return nil, nil, err
		}

		def, err := ctr.Marshal(ctx, pc)
		if err != nil {
			return nil, nil, err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, err
		}

		img, err := linux.BuildImageConfig(ctx, sOpt, spec, platform, targetKey)
		if err != nil {
			return nil, nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}

		return ref, img, nil
	})
}
