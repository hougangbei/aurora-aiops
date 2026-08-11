package deployment

import (
	"sort"
	"sync"
)

type Catalog struct {
	mu         sync.RWMutex
	installers map[string]Installer
}

func (c *Catalog) Register(installer Installer) error {
	if installer == nil {
		return ErrInvalidInput
	}
	project := installer.Project()
	if project.ID == "" || len(project.SupportedArchitectures) == 0 || !strictlySorted(project.Versions) {
		return ErrInvalidInput
	}
	project = cloneProject(project)
	sort.Strings(project.SupportedOSFamilies)
	sort.Strings(project.SupportedArchitectures)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.installers == nil {
		c.installers = make(map[string]Installer)
	}
	if _, exists := c.installers[project.ID]; exists {
		return ErrInvalidInput
	}
	c.installers[project.ID] = catalogInstaller{Installer: installer, project: project}
	return nil
}

func (c *Catalog) Get(id string) (Project, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	installer, ok := c.installers[id]
	if !ok {
		return Project{}, false
	}
	return cloneProject(installer.Project()), true
}

// Installer returns the registered implementation for worker execution. The
// catalog owns the registration and callers cannot mutate its project copy.
func (c *Catalog) Installer(id string) (Installer, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	installer, ok := c.installers[id]
	if !ok {
		return nil, false
	}
	return installer, true
}

func (c *Catalog) List() []Project {
	c.mu.RLock()
	defer c.mu.RUnlock()
	projects := make([]Project, 0, len(c.installers))
	for _, installer := range c.installers {
		projects = append(projects, cloneProject(installer.Project()))
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
	return projects
}

type catalogInstaller struct {
	Installer
	project Project
}

func (i catalogInstaller) Project() Project { return cloneProject(i.project) }

func strictlySorted(values []string) bool {
	for i := range values {
		if values[i] == "" || (i > 0 && values[i-1] >= values[i]) {
			return false
		}
	}
	return true
}
func cloneProject(p Project) Project {
	p.Versions = append([]string(nil), p.Versions...)
	p.SupportedOSFamilies = append([]string(nil), p.SupportedOSFamilies...)
	p.SupportedArchitectures = append([]string(nil), p.SupportedArchitectures...)
	return p
}
